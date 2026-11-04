//! Haat market contract.
//!
//! One NEP-245 multi token contract holds the village's money (kauri), its
//! goods and the shares of every shop. Players gather, craft and trade here;
//! shopkeeper agents sign their own policy changes, city trips and daily
//! settlements from their own accounts. All pricing is in `pricing.rs`.

use near_sdk::json_types::U128;
use near_sdk::store::{IterableMap, LookupMap, Vector};
use near_sdk::{env, near, require, AccountId, BorshStorageKey, PanicOnDefault};

mod events;
mod mt;
pub mod pricing;
pub mod world;

use pricing::{Levers, Shelf};
use world::{season_of, ENERGY_PER_DAY};

/// The village currency.
pub const KAURI: &str = "kauri";
/// Every item in the game. Pickle is crafted, the rest are gathered.
pub const ITEMS: [&str; 5] = ["mango", "rice", "fish", "chilli", "pickle"];
/// Share tokens are named "share:<merchant account>".
pub const SHARE_PREFIX: &str = "share:";
/// Kauri a new player starts with.
pub const STARTER_PURSE: u128 = 400;
/// A shop with less than this many kauri is closed.
pub const CLOSE_BELOW: u128 = 200;
/// Shares that stand for the shopkeeper's own stake. Never minted.
pub const FOUNDER_SHARES: u128 = 1_000;
/// Shop value used for share pricing never goes below this.
pub const MIN_SHOP_VALUE: u128 = 500;
/// Share of a day's profit paid out, before the investors' fraction is applied.
pub const DIVIDEND_BPS: u128 = 5_000;
/// Fixed-point scale for the dividend accumulator.
pub const ACC_SCALE: u128 = 1_000_000_000_000;
/// Largest number of units in one trade.
pub const MAX_TRADE: u128 = 50;
/// Smallest investment.
pub const MIN_INVEST: u128 = 10;
/// Longest note a shopkeeper can post.
pub const MAX_NOTE: usize = 200;

#[derive(BorshStorageKey)]
#[near]
enum StorageKey {
    Balances,
    Supply,
    Merchants,
    Players,
    Holdings,
    History,
    Chronicle,
}

/// One shopkeeper lever setting for one item.
#[near(serializers = [borsh, json])]
#[derive(Clone, Debug, PartialEq)]
pub struct Policy {
    pub token_id: String,
    pub target_stock: u32,
    pub margin_bps: u32,
}

/// Units players bought from a shop, with lazy daily decay.
#[near(serializers = [borsh])]
#[derive(Clone, Default)]
pub struct Demand {
    day: u32,
    today: u32,
    recent: u32,
}

impl Demand {
    /// Demand as seen on `day`: earlier days fade by half per day.
    fn recent_on(&self, day: u32) -> u128 {
        if self.day >= day {
            return (self.recent + self.today) as u128;
        }
        let mut r = pricing::roll_demand(self.recent as u128, self.today as u128);
        for _ in 1..(day - self.day).min(32) {
            r /= 2;
        }
        r
    }

    fn record(&mut self, day: u32, units: u32) {
        if self.day < day {
            self.recent = self.recent_on(day) as u32;
            self.today = 0;
            self.day = day;
        }
        self.today += units;
    }
}

#[near(serializers = [borsh])]
#[derive(Clone)]
pub struct Merchant {
    name: String,
    policies: Vec<Policy>,
    demand: Vec<Demand>,
    note: String,
    note_day: u32,
    shares_issued: u128,
    acc_per_share: u128,
    dividend_pool: u128,
    last_mark: u128,
    last_settle_day: Option<u32>,
    open: bool,
}

#[near(serializers = [borsh])]
#[derive(Clone, Default)]
pub struct Player {
    energy_day: u32,
    energy_used: u32,
    actions: u64,
}

#[near(serializers = [borsh])]
#[derive(Clone, Default)]
pub struct Holding {
    paid_acc: u128,
    owed: u128,
}

#[near(serializers = [borsh, json])]
#[derive(Clone)]
pub struct ShelfSnap {
    pub token_id: String,
    pub stock: U128,
    pub ask: Option<U128>,
    pub bid: U128,
}

#[near(serializers = [borsh, json])]
#[derive(Clone)]
pub struct ShopSnap {
    pub merchant_id: AccountId,
    pub kauri: U128,
    pub open: bool,
    pub shelves: Vec<ShelfSnap>,
}

/// End-of-day prices and purses, written by `advance_day`.
#[near(serializers = [borsh, json])]
#[derive(Clone)]
pub struct DaySnapshot {
    pub day: u32,
    pub flood: bool,
    pub shops: Vec<ShopSnap>,
}

/// A line in the village chronicle: bankruptcies, rescues, floods, seasons.
#[near(serializers = [borsh, json])]
#[derive(Clone)]
pub struct ChronicleEntry {
    pub day: u32,
    pub kind: String,
    pub merchant_id: Option<AccountId>,
    pub account_id: Option<AccountId>,
    pub amount: Option<U128>,
}

#[near(contract_state)]
#[derive(PanicOnDefault)]
pub struct Market {
    owner_id: AccountId,
    world_seed: u64,
    day: u32,
    balances: LookupMap<(String, AccountId), u128>,
    supply: IterableMap<String, u128>,
    merchants: IterableMap<AccountId, Merchant>,
    players: LookupMap<AccountId, Player>,
    player_count: u32,
    holdings: LookupMap<(AccountId, AccountId), Holding>,
    history: Vector<DaySnapshot>,
    chronicle: Vector<ChronicleEntry>,
}

// ---------- JSON views ----------

#[near(serializers = [json])]
pub struct ForecastDay {
    pub day: u32,
    pub season: String,
    pub flood: bool,
}

#[near(serializers = [json])]
pub struct WorldView {
    pub day: u32,
    pub season: String,
    pub flood: bool,
    pub forecast: Vec<ForecastDay>,
    pub city_prices: Vec<(String, U128)>,
    pub players: u32,
    pub owner_id: AccountId,
}

#[near(serializers = [json])]
pub struct ShelfView {
    pub token_id: String,
    pub stock: U128,
    pub target_stock: u32,
    pub margin_bps: u32,
    pub recent_demand: U128,
    pub ask: Option<U128>,
    pub bid: U128,
}

#[near(serializers = [json])]
pub struct MerchantView {
    pub account_id: AccountId,
    pub name: String,
    pub open: bool,
    pub kauri: U128,
    pub value: U128,
    pub shares_issued: U128,
    pub dividend_pool: U128,
    pub note: String,
    pub note_day: u32,
    pub settled_today: bool,
    pub shelves: Vec<ShelfView>,
    pub stock: Vec<(String, U128)>,
}

#[near(serializers = [json])]
pub struct QuoteView {
    pub merchant_id: AccountId,
    pub token_id: String,
    pub side: String,
    pub amount: U128,
    pub total: Option<U128>,
    pub units: Vec<U128>,
    pub city_price: U128,
    pub stock: U128,
    pub target_stock: u32,
    pub margin_bps: u32,
    pub recent_demand: U128,
    pub stock_factor_bps: U128,
    pub demand_factor_bps: U128,
    pub merchant_kauri: U128,
    pub open: bool,
}

#[near(serializers = [json])]
pub struct HoldingView {
    pub merchant_id: AccountId,
    pub shares: U128,
    pub pending_dividends: U128,
    pub stake_bps: U128,
}

#[near(serializers = [json])]
pub struct PlayerView {
    pub account_id: AccountId,
    pub registered: bool,
    pub energy_left: u32,
    pub balances: Vec<(String, U128)>,
    pub holdings: Vec<HoldingView>,
}

#[near]
impl Market {
    #[init]
    pub fn new(owner_id: AccountId, world_seed: u64) -> Self {
        let mut m = Self {
            owner_id,
            world_seed,
            day: 0,
            balances: LookupMap::new(StorageKey::Balances),
            supply: IterableMap::new(StorageKey::Supply),
            merchants: IterableMap::new(StorageKey::Merchants),
            players: LookupMap::new(StorageKey::Players),
            player_count: 0,
            holdings: LookupMap::new(StorageKey::Holdings),
            history: Vector::new(StorageKey::History),
            chronicle: Vector::new(StorageKey::Chronicle),
        };
        for t in std::iter::once(KAURI).chain(ITEMS) {
            m.supply.insert(t.to_string(), 0);
        }
        m.log_chronicle("season_garmi", None, None, None);
        m
    }

    // ---------- owner ----------

    /// Opens a shop. The shop's account will sign its own policies from now on.
    pub fn add_merchant(
        &mut self,
        account_id: AccountId,
        name: String,
        items: Vec<String>,
        opening_kauri: U128,
        opening_stock: Vec<U128>,
    ) {
        self.assert_owner();
        require!(self.merchants.get(&account_id).is_none(), "merchant already exists");
        require!(!items.is_empty() && items.len() <= ITEMS.len(), "a shop trades 1 to 5 items");
        require!(items.len() == opening_stock.len(), "one opening stock per item");
        for (i, t) in items.iter().enumerate() {
            require!(ITEMS.contains(&t.as_str()), format!("unknown item {t}"));
            require!(!items[..i].contains(t), "duplicate item");
        }
        let merchant = Merchant {
            name,
            policies: items
                .iter()
                .map(|t| Policy { token_id: t.clone(), target_stock: 20, margin_bps: 1_000 })
                .collect(),
            demand: vec![Demand::default(); items.len()],
            note: String::new(),
            note_day: self.day,
            shares_issued: 0,
            acc_per_share: 0,
            dividend_pool: 0,
            last_mark: opening_kauri.0,
            last_settle_day: None,
            open: opening_kauri.0 >= CLOSE_BELOW,
        };
        self.merchants.insert(account_id.clone(), merchant);
        self.supply.insert(share_token(&account_id), 0);
        self.internal_mint(KAURI, &account_id, opening_kauri.0, Some("opening purse"));
        for (t, s) in items.iter().zip(opening_stock) {
            self.internal_mint(t, &account_id, s.0, Some("opening stock"));
        }
        self.log_chronicle("shop_opened", Some(account_id), None, Some(opening_kauri.0));
    }

    /// Gives a new player the starter purse. Safe to call twice.
    pub fn register_player(&mut self, account_id: AccountId) -> bool {
        self.assert_owner();
        if self.players.get(&account_id).is_some() {
            return false;
        }
        self.players.insert(account_id.clone(), Player { energy_day: self.day, ..Default::default() });
        self.player_count += 1;
        self.internal_mint(KAURI, &account_id, STARTER_PURSE, Some("starter purse"));
        true
    }

    /// Ends the day: records every shop's prices and purse, then moves the calendar on.
    pub fn advance_day(&mut self) -> u32 {
        self.assert_owner();
        let snapshot = self.snapshot();
        self.history.push(snapshot);
        let was_flood = world::is_flood(self.world_seed, self.day);
        let old_season = season_of(self.day);
        self.day += 1;
        let season = season_of(self.day);
        if season != old_season {
            let kind = format!("season_{}", season.name());
            self.log_chronicle(&kind, None, None, None);
        }
        let flood = world::is_flood(self.world_seed, self.day);
        if flood && !was_flood {
            self.log_chronicle("flood", None, None, None);
        } else if was_flood && !flood {
            self.log_chronicle("road_open", None, None, None);
        }
        events::haat("day", near_sdk::serde_json::json!({ "day": self.day, "season": season.name(), "flood": flood }));
        self.day
    }

    // ---------- players ----------

    /// Spends one energy at a place and adds what it yields to the basket.
    pub fn gather(&mut self, place: String) -> U128 {
        let who = env::predecessor_account_id();
        let item = world::place_item(&place).unwrap_or_else(|| env::panic_str("unknown place"));
        let mut p = self.players.get(&who).cloned().unwrap_or_else(|| env::panic_str("not a registered player"));
        if p.energy_day != self.day {
            p.energy_day = self.day;
            p.energy_used = 0;
        }
        require!(p.energy_used < ENERGY_PER_DAY, "no energy left today, rest until tomorrow");
        p.energy_used += 1;
        p.actions += 1;
        let account_hash = u64::from_le_bytes(env::sha256_array(who.as_bytes())[..8].try_into().unwrap());
        let luck = world::mix(self.world_seed ^ account_hash ^ ((self.day as u64) << 40) ^ p.actions);
        let amount = world::gather_yield(item, season_of(self.day), luck) as u128;
        self.players.insert(who.clone(), p);
        self.internal_mint(item, &who, amount, Some(&format!("gathered at {place}")));
        U128(amount)
    }

    /// Makes pickle: 2 mango and 1 chilli become 1 pickle.
    pub fn craft(&mut self, recipe: String, amount: U128) -> U128 {
        require!(recipe == "pickle", "the only recipe is pickle");
        let n = amount.0;
        require!(n >= 1 && n <= MAX_TRADE, "craft 1 to 50 at a time");
        let who = env::predecessor_account_id();
        self.internal_burn("mango", &who, 2 * n, Some("pickle"));
        self.internal_burn("chilli", &who, n, Some("pickle"));
        self.internal_mint("pickle", &who, n, Some("pickle"));
        U128(n)
    }

    /// Sells items to a shop. Fails if the shop would pay less than `min_total`.
    pub fn sell(&mut self, merchant_id: AccountId, token_id: String, amount: U128, min_total: U128) -> U128 {
        let who = env::predecessor_account_id();
        require!(who != merchant_id, "a shop cannot trade with itself");
        require!(amount.0 >= 1 && amount.0 <= MAX_TRADE, "trade 1 to 50 units at a time");
        let (m, idx) = self.open_shop(&merchant_id, &token_id);
        let shelf = self.shelf(&merchant_id, &m, idx);
        let (total, _) = pricing::quote_shop_buys(&shelf, amount.0);
        require!(total > 0, format!("{} will not buy any more {token_id}", m.name));
        require!(total >= min_total.0, format!("price moved: {} offers {total} kauri", m.name));
        let purse = self.balance(KAURI, &merchant_id);
        require!(purse >= total, format!("{} cannot afford that: has {purse} kauri", m.name));
        self.internal_transfer(&token_id, &who, &merchant_id, amount.0, Some("sell"));
        self.internal_transfer(KAURI, &merchant_id, &who, total, Some("sell"));
        events::haat(
            "trade",
            near_sdk::serde_json::json!({ "player": who, "merchant": merchant_id, "side": "sell", "token_id": token_id, "amount": amount, "total": U128(total) }),
        );
        self.refresh_open(&merchant_id, None);
        U128(total)
    }

    /// Buys items from a shop. Fails if it would cost more than `max_total`.
    pub fn buy(&mut self, merchant_id: AccountId, token_id: String, amount: U128, max_total: U128) -> U128 {
        let who = env::predecessor_account_id();
        require!(who != merchant_id, "a shop cannot trade with itself");
        require!(amount.0 >= 1 && amount.0 <= MAX_TRADE, "trade 1 to 50 units at a time");
        let (mut m, idx) = self.open_shop(&merchant_id, &token_id);
        let shelf = self.shelf(&merchant_id, &m, idx);
        let (total, _) = pricing::quote_shop_sells(&shelf, amount.0)
            .unwrap_or_else(|| env::panic_str(&format!("{} has only {} {token_id}", m.name, shelf.stock)));
        require!(total <= max_total.0, format!("price moved: {} asks {total} kauri", m.name));
        self.internal_transfer(KAURI, &who, &merchant_id, total, Some("buy"));
        self.internal_transfer(&token_id, &merchant_id, &who, amount.0, Some("buy"));
        m.demand[idx].record(self.day, amount.0 as u32);
        self.merchants.insert(merchant_id.clone(), m);
        events::haat(
            "trade",
            near_sdk::serde_json::json!({ "player": who, "merchant": merchant_id, "side": "buy", "token_id": token_id, "amount": amount, "total": U128(total) }),
        );
        self.refresh_open(&merchant_id, None);
        U128(total)
    }

    // ---------- shopkeepers ----------

    /// A shopkeeper sets its target stock and margin for each item, with a note
    /// explaining why. Only the shop's own account can call this.
    pub fn set_policies(&mut self, policies: Vec<Policy>, note: String) {
        let me = env::predecessor_account_id();
        let mut m = self.merchant(&me);
        require!(note.chars().count() <= MAX_NOTE, "note is too long");
        for p in &policies {
            require!((1..=200).contains(&p.target_stock), "target stock must be 1 to 200");
            require!((200..=4_000).contains(&p.margin_bps), "margin must be 200 to 4000 bps");
            let slot = m
                .policies
                .iter_mut()
                .find(|q| q.token_id == p.token_id)
                .unwrap_or_else(|| env::panic_str(&format!("shop does not trade {}", p.token_id)));
            *slot = p.clone();
        }
        m.note = note;
        m.note_day = self.day;
        self.merchants.insert(me, m);
    }

    /// Sells goods to the city cart at 80% of the city price. Not on flood days.
    pub fn city_sell(&mut self, token_id: String, amount: U128) -> U128 {
        let me = env::predecessor_account_id();
        self.merchant(&me);
        require!(!world::is_flood(self.world_seed, self.day), "the road is flooded, the city cart did not come");
        require!(amount.0 >= 1 && amount.0 <= MAX_TRADE, "trade 1 to 50 units at a time");
        let unit = pricing::city_bid(&token_id, season_of(self.day));
        require!(pricing::base_price(&token_id).is_some(), "unknown item");
        let total = unit * amount.0;
        self.internal_burn(&token_id, &me, amount.0, Some("city cart"));
        self.internal_mint(KAURI, &me, total, Some("city cart"));
        self.refresh_open(&me, None);
        U128(total)
    }

    /// Buys goods from the city cart at 125% of the city price. Not on flood days.
    pub fn city_buy(&mut self, token_id: String, amount: U128) -> U128 {
        let me = env::predecessor_account_id();
        self.merchant(&me);
        require!(!world::is_flood(self.world_seed, self.day), "the road is flooded, the city cart did not come");
        require!(amount.0 >= 1 && amount.0 <= MAX_TRADE, "trade 1 to 50 units at a time");
        require!(pricing::base_price(&token_id).is_some(), "unknown item");
        let total = pricing::city_ask(&token_id, season_of(self.day)) * amount.0;
        self.internal_burn(KAURI, &me, total, Some("city cart"));
        self.internal_mint(&token_id, &me, amount.0, Some("city cart"));
        self.refresh_open(&me, None);
        U128(total)
    }

    /// Closes the shop's books for the day. If its purse grew, half of the profit,
    /// times the investors' fraction of all shares, goes to the dividend pool.
    pub fn settle_day(&mut self) -> U128 {
        let me = env::predecessor_account_id();
        let mut m = self.merchant(&me);
        require!(m.last_settle_day != Some(self.day), "already settled today");
        let purse = self.balance(KAURI, &me);
        let profit = purse.saturating_sub(m.last_mark);
        let mut dividend = 0;
        if profit > 0 && m.shares_issued > 0 {
            dividend = profit * DIVIDEND_BPS / pricing::BPS * m.shares_issued / (FOUNDER_SHARES + m.shares_issued);
        }
        m.last_settle_day = Some(self.day);
        m.last_mark = purse - dividend;
        if dividend > 0 {
            m.acc_per_share += dividend * ACC_SCALE / m.shares_issued;
            m.dividend_pool += dividend;
        }
        self.merchants.insert(me.clone(), m);
        if dividend > 0 {
            self.internal_transfer(KAURI, &me, &env::current_account_id(), dividend, Some("dividend pool"));
            events::haat("dividend", near_sdk::serde_json::json!({ "merchant": me, "day": self.day, "profit": U128(profit), "dividend": U128(dividend) }));
            self.log_chronicle("dividend", Some(me.clone()), None, Some(dividend));
        }
        self.refresh_open(&me, None);
        U128(dividend)
    }

    // ---------- investors ----------

    /// Invests kauri in a shop for newly minted shares. A shop that is worth
    /// little sells its shares cheaply, so rescuing a broke shop buys a big stake.
    pub fn invest(&mut self, merchant_id: AccountId, amount: U128) -> U128 {
        let who = env::predecessor_account_id();
        require!(who != merchant_id, "a shop cannot invest in itself");
        require!(amount.0 >= MIN_INVEST, "invest at least 10 kauri");
        let m = self.merchant(&merchant_id);
        let value = self.shop_value(&merchant_id, &m).max(MIN_SHOP_VALUE);
        let shares = amount.0 * (FOUNDER_SHARES + m.shares_issued) / value;
        require!(shares > 0, "investment too small for one share");
        self.internal_transfer(KAURI, &who, &merchant_id, amount.0, Some("investment"));
        self.internal_mint(&share_token(&merchant_id), &who, shares, Some("investment"));
        let mut m = self.merchant(&merchant_id);
        m.shares_issued += shares;
        m.last_mark += amount.0;
        self.merchants.insert(merchant_id.clone(), m);
        events::haat(
            "invest",
            near_sdk::serde_json::json!({ "investor": who, "merchant": merchant_id, "amount": amount, "shares": U128(shares) }),
        );
        self.log_chronicle("invest", Some(merchant_id.clone()), Some(who.clone()), Some(amount.0));
        self.refresh_open(&merchant_id, Some(who));
        U128(shares)
    }

    /// Pays out the caller's accrued dividends from one shop.
    pub fn claim_dividends(&mut self, merchant_id: AccountId) -> U128 {
        let who = env::predecessor_account_id();
        self.settle_holder(&merchant_id, &who);
        let key = (merchant_id.clone(), who.clone());
        let mut h = self.holdings.get(&key).cloned().unwrap_or_default();
        let owed = h.owed;
        if owed == 0 {
            return U128(0);
        }
        h.owed = 0;
        self.holdings.insert(key, h);
        let mut m = self.merchant(&merchant_id);
        m.dividend_pool = m.dividend_pool.saturating_sub(owed);
        self.merchants.insert(merchant_id.clone(), m);
        self.internal_transfer(KAURI, &env::current_account_id(), &who, owed, Some("dividend"));
        U128(owed)
    }

    // ---------- views ----------

    pub fn get_world(&self) -> WorldView {
        let season = season_of(self.day);
        WorldView {
            day: self.day,
            season: season.name().to_string(),
            flood: world::is_flood(self.world_seed, self.day),
            forecast: (1..=3)
                .map(|d| ForecastDay {
                    day: self.day + d,
                    season: season_of(self.day + d).name().to_string(),
                    flood: world::is_flood(self.world_seed, self.day + d),
                })
                .collect(),
            city_prices: ITEMS.iter().map(|t| (t.to_string(), U128(pricing::city_price(t, season)))).collect(),
            players: self.player_count,
            owner_id: self.owner_id.clone(),
        }
    }

    pub fn get_merchants(&self) -> Vec<MerchantView> {
        self.merchants.iter().map(|(id, m)| self.merchant_view(id, m)).collect()
    }

    pub fn get_merchant(&self, account_id: AccountId) -> Option<MerchantView> {
        self.merchants.get(&account_id).map(|m| self.merchant_view(&account_id, m))
    }

    /// Prices a trade without making it. `side` is "sell" (player sells to the
    /// shop) or "buy" (player buys from the shop). Includes every factor used.
    pub fn quote(&self, merchant_id: AccountId, token_id: String, side: String, amount: U128) -> QuoteView {
        let m = self.merchant(&merchant_id);
        let idx = m
            .policies
            .iter()
            .position(|p| p.token_id == token_id)
            .unwrap_or_else(|| env::panic_str("shop does not trade that item"));
        let shelf = self.shelf(&merchant_id, &m, idx);
        let (total, units) = match side.as_str() {
            "sell" => {
                let (t, u) = pricing::quote_shop_buys(&shelf, amount.0);
                (Some(t), u)
            }
            "buy" => match pricing::quote_shop_sells(&shelf, amount.0) {
                Some((t, u)) => (Some(t), u),
                None => (None, vec![]),
            },
            _ => env::panic_str("side must be sell or buy"),
        };
        QuoteView {
            merchant_id: merchant_id.clone(),
            token_id,
            side,
            amount,
            total: total.map(U128),
            units: units.into_iter().map(U128).collect(),
            city_price: U128(shelf.city),
            stock: U128(shelf.stock),
            target_stock: shelf.levers.target as u32,
            margin_bps: shelf.levers.margin_bps as u32,
            recent_demand: U128(shelf.recent_buys),
            stock_factor_bps: U128(pricing::stock_factor_bps(shelf.stock, shelf.levers.target)),
            demand_factor_bps: U128(pricing::demand_factor_bps(shelf.recent_buys)),
            merchant_kauri: U128(self.balance(KAURI, &merchant_id)),
            open: m.open,
        }
    }

    pub fn get_player(&self, account_id: AccountId) -> PlayerView {
        let p = self.players.get(&account_id);
        let energy_left = match p {
            Some(p) if p.energy_day == self.day => ENERGY_PER_DAY - p.energy_used,
            Some(_) => ENERGY_PER_DAY,
            None => 0,
        };
        let balances = std::iter::once(KAURI)
            .chain(ITEMS)
            .map(|t| (t.to_string(), U128(self.balance(t, &account_id))))
            .collect();
        let holdings = self
            .merchants
            .iter()
            .filter_map(|(mid, m)| {
                let shares = self.balance(&share_token(mid), &account_id);
                let pending = self.pending_dividends(mid, m, &account_id);
                if shares == 0 && pending == 0 {
                    return None;
                }
                Some(HoldingView {
                    merchant_id: mid.clone(),
                    shares: U128(shares),
                    pending_dividends: U128(pending),
                    stake_bps: U128(shares * pricing::BPS / (FOUNDER_SHARES + m.shares_issued)),
                })
            })
            .collect();
        PlayerView { account_id, registered: p.is_some(), energy_left, balances, holdings }
    }

    /// Daily snapshots from `from_day`, at most `limit` of them.
    pub fn history(&self, from_day: u32, limit: u32) -> Vec<DaySnapshot> {
        self.history.iter().skip(from_day as usize).take(limit.min(60) as usize).cloned().collect()
    }

    /// The live snapshot for today, in the same shape as `history`.
    pub fn today(&self) -> DaySnapshot {
        self.snapshot()
    }

    pub fn chronicle(&self, from_index: u32, limit: u32) -> Vec<ChronicleEntry> {
        self.chronicle.iter().skip(from_index as usize).take(limit.min(100) as usize).cloned().collect()
    }

    pub fn chronicle_len(&self) -> u32 {
        self.chronicle.len()
    }
}

// ---------- internals ----------

pub fn share_token(merchant_id: &AccountId) -> String {
    format!("{SHARE_PREFIX}{merchant_id}")
}

impl Market {
    fn assert_owner(&self) {
        require!(env::predecessor_account_id() == self.owner_id, "only the owner can do this");
    }

    fn merchant(&self, id: &AccountId) -> Merchant {
        self.merchants.get(id).cloned().unwrap_or_else(|| env::panic_str("no such shop"))
    }

    fn open_shop(&self, id: &AccountId, token_id: &str) -> (Merchant, usize) {
        let m = self.merchant(id);
        require!(m.open, format!("{}'s shop is closed until someone invests", m.name));
        let idx = m
            .policies
            .iter()
            .position(|p| p.token_id == token_id)
            .unwrap_or_else(|| env::panic_str(&format!("{} does not trade {token_id}", m.name)));
        (m, idx)
    }

    fn shelf(&self, id: &AccountId, m: &Merchant, idx: usize) -> Shelf {
        let p = &m.policies[idx];
        Shelf {
            city: pricing::city_price(&p.token_id, season_of(self.day)),
            stock: self.balance(&p.token_id, id),
            recent_buys: m.demand[idx].recent_on(self.day),
            levers: Levers { target: p.target_stock as u128, margin_bps: p.margin_bps as u128 },
        }
    }

    fn shop_value(&self, id: &AccountId, _m: &Merchant) -> u128 {
        let season = season_of(self.day);
        let goods: u128 = ITEMS.iter().map(|t| self.balance(t, id) * pricing::city_price(t, season)).sum();
        self.balance(KAURI, id) + goods
    }

    fn merchant_view(&self, id: &AccountId, m: &Merchant) -> MerchantView {
        let shelves = (0..m.policies.len())
            .map(|i| {
                let s = self.shelf(id, m, i);
                ShelfView {
                    token_id: m.policies[i].token_id.clone(),
                    stock: U128(s.stock),
                    target_stock: m.policies[i].target_stock,
                    margin_bps: m.policies[i].margin_bps,
                    recent_demand: U128(s.recent_buys),
                    ask: pricing::ask_one(&s).map(U128),
                    bid: U128(pricing::bid_one(&s)),
                }
            })
            .collect();
        MerchantView {
            account_id: id.clone(),
            name: m.name.clone(),
            open: m.open,
            kauri: U128(self.balance(KAURI, id)),
            value: U128(self.shop_value(id, m)),
            shares_issued: U128(m.shares_issued),
            dividend_pool: U128(m.dividend_pool),
            note: m.note.clone(),
            note_day: m.note_day,
            settled_today: m.last_settle_day == Some(self.day),
            shelves,
            stock: ITEMS
                .iter()
                .map(|t| (t.to_string(), U128(self.balance(t, id))))
                .filter(|(_, b)| b.0 > 0)
                .collect(),
        }
    }

    fn snapshot(&self) -> DaySnapshot {
        DaySnapshot {
            day: self.day,
            flood: world::is_flood(self.world_seed, self.day),
            shops: self
                .merchants
                .iter()
                .map(|(id, m)| ShopSnap {
                    merchant_id: id.clone(),
                    kauri: U128(self.balance(KAURI, id)),
                    open: m.open,
                    shelves: (0..m.policies.len())
                        .map(|i| {
                            let s = self.shelf(id, m, i);
                            ShelfSnap {
                                token_id: m.policies[i].token_id.clone(),
                                stock: U128(s.stock),
                                ask: pricing::ask_one(&s).map(U128),
                                bid: U128(pricing::bid_one(&s)),
                            }
                        })
                        .collect(),
                })
                .collect(),
        }
    }

    /// Opens or closes a shop to match its purse and records the change.
    fn refresh_open(&mut self, id: &AccountId, actor: Option<AccountId>) {
        let mut m = self.merchant(id);
        let purse = self.balance(KAURI, id);
        let open = purse >= CLOSE_BELOW;
        if open == m.open {
            return;
        }
        m.open = open;
        self.merchants.insert(id.clone(), m);
        let kind = if open { "shop_reopened" } else { "shop_closed" };
        events::haat(kind, near_sdk::serde_json::json!({ "merchant": id, "day": self.day, "kauri": U128(purse) }));
        self.log_chronicle(kind, Some(id.clone()), actor, Some(purse));
    }

    fn pending_dividends(&self, merchant_id: &AccountId, m: &Merchant, holder: &AccountId) -> u128 {
        let h = self.holdings.get(&(merchant_id.clone(), holder.clone())).cloned().unwrap_or_default();
        let shares = self.balance(&share_token(merchant_id), holder);
        h.owed + shares * (m.acc_per_share - h.paid_acc) / ACC_SCALE
    }

    /// Brings a holder's dividend checkpoint up to date. Must run before their share balance changes.
    pub(crate) fn settle_holder(&mut self, merchant_id: &AccountId, holder: &AccountId) {
        let Some(m) = self.merchants.get(merchant_id) else { return };
        let acc = m.acc_per_share;
        let key = (merchant_id.clone(), holder.clone());
        let mut h = self.holdings.get(&key).cloned().unwrap_or_default();
        if h.paid_acc == acc {
            return;
        }
        let shares = self.balance(&share_token(merchant_id), holder);
        h.owed += shares * (acc - h.paid_acc) / ACC_SCALE;
        h.paid_acc = acc;
        self.holdings.insert(key, h);
    }

    fn log_chronicle(&mut self, kind: &str, merchant_id: Option<AccountId>, account_id: Option<AccountId>, amount: Option<u128>) {
        self.chronicle.push(ChronicleEntry {
            day: self.day,
            kind: kind.to_string(),
            merchant_id,
            account_id,
            amount: amount.map(U128),
        });
    }
}

#[cfg(test)]
mod tests;
