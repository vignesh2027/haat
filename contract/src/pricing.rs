//! How every price in the haat is set.
//!
//! This is the one file to read to understand the economy. The contract calls
//! these functions on every trade, and the game shows the same numbers in the
//! tooltip next to each price, so what a player reads is what they pay.
//!
//! Money is counted in kauri (cowrie shells). Prices are whole kauri.
//!
//! In plain words:
//!
//! 1. Every item has a city price, which changes with the season.
//! 2. A shop wants to hold a target stock of each item it trades. When it has
//!    less than its target it pays more, up to twice the city price when it is
//!    empty. When it has more it pays less, and the price keeps falling as the
//!    stall floods.
//! 3. When players have been buying an item from a shop, the shop charges up to
//!    50% more for it. That demand fades by half every day.
//! 4. The shop keeps a margin: it asks a little above that fair value and bids
//!    a little below it. Each shopkeeper picks its own target and margin.
//! 5. A trade of many units is priced one unit at a time, so every extra mango
//!    you dump on a stall is worth less than the one before.
//! 6. The city cart buys at 80% of the city price and sells at 125%, and only
//!    comes on days the road is not flooded.

use crate::world::Season;

/// Basis points: 10_000 means 100%.
pub const BPS: u128 = 10_000;

/// City price of each item in kauri, before the season is applied.
pub fn base_price(item: &str) -> Option<u128> {
    match item {
        "mango" => Some(60),
        "rice" => Some(50),
        "fish" => Some(70),
        "chilli" => Some(80),
        "pickle" => Some(300),
        _ => None,
    }
}

/// How the season moves city prices, in percent.
pub fn season_pct(item: &str, season: Season) -> u128 {
    match (season, item) {
        (Season::Garmi, "mango") => 80,
        (Season::Barsaat, "fish") => 70,
        (Season::Barsaat, "chilli") => 140,
        (Season::Barsaat, "mango") => 150,
        (Season::Tyohar, "rice") => 80,
        (Season::Tyohar, "pickle") => 200,
        _ => 100,
    }
}

/// City price of an item today, in kauri.
pub fn city_price(item: &str, season: Season) -> u128 {
    let base = base_price(item).unwrap_or(0);
    (base * season_pct(item, season) / 100).max(1)
}

/// What the city cart pays a shop for one unit (80% of the city price).
pub fn city_bid(item: &str, season: Season) -> u128 {
    (city_price(item, season) * 80 / 100).max(1)
}

/// What the city cart charges a shop for one unit (125% of the city price).
pub fn city_ask(item: &str, season: Season) -> u128 {
    (city_price(item, season) * 125).div_ceil(100)
}

/// Stock factor in basis points: 2T / (T + s).
/// 20_000 (2x) when the stall is empty, 10_000 (1x) at target, towards 0 when flooded.
pub fn stock_factor_bps(stock: u128, target: u128) -> u128 {
    let t = target.max(1);
    2 * BPS * t / (t + stock)
}

/// Demand factor in basis points: +5% per unit players bought recently, at most +50%.
pub fn demand_factor_bps(recent_buys: u128) -> u128 {
    BPS + (recent_buys * 500).min(5_000)
}

/// Fair value of one unit in milli-kauri (thousandths), before the margin.
pub fn fair_milli(city: u128, stock: u128, target: u128, recent_buys: u128) -> u128 {
    city * 1_000 * stock_factor_bps(stock, target) / BPS * demand_factor_bps(recent_buys) / BPS
}

/// The levers a shopkeeper controls for one item.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Levers {
    pub target: u128,
    pub margin_bps: u128,
}

/// The state of one item at one shop that pricing depends on.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Shelf {
    pub city: u128,
    pub stock: u128,
    pub recent_buys: u128,
    pub levers: Levers,
}

/// Price the shop asks for its next unit. The stock used is the stock after
/// the sale, so the last mango on the shelf is the dearest. None when the
/// shelf is empty.
pub fn ask_one(shelf: &Shelf) -> Option<u128> {
    if shelf.stock == 0 {
        return None;
    }
    let fair = fair_milli(
        shelf.city,
        shelf.stock - 1,
        shelf.levers.target,
        shelf.recent_buys,
    );
    Some(
        (fair * (BPS + shelf.levers.margin_bps))
            .div_ceil(BPS * 1_000)
            .max(1),
    )
}

/// Price the shop bids for the next unit a player sells. The stock used is the
/// stock after the purchase. Zero means the shop will not buy any more.
pub fn bid_one(shelf: &Shelf) -> u128 {
    let fair = fair_milli(
        shelf.city,
        shelf.stock + 1,
        shelf.levers.target,
        shelf.recent_buys,
    );
    fair * (BPS - shelf.levers.margin_bps.min(BPS)) / (BPS * 1_000)
}

/// Total a shop pays for `amount` units, priced one at a time, and the price of each unit.
pub fn quote_shop_buys(shelf: &Shelf, amount: u128) -> (u128, Vec<u128>) {
    let mut s = *shelf;
    let mut units = Vec::new();
    let mut total = 0;
    for _ in 0..amount {
        let p = bid_one(&s);
        units.push(p);
        total += p;
        s.stock += 1;
    }
    (total, units)
}

/// Total a shop charges for `amount` units, priced one at a time. None if it
/// does not have that many. Each unit bought also counts as demand.
pub fn quote_shop_sells(shelf: &Shelf, amount: u128) -> Option<(u128, Vec<u128>)> {
    if amount > shelf.stock {
        return None;
    }
    let mut s = *shelf;
    let mut units = Vec::new();
    let mut total = 0;
    for _ in 0..amount {
        let p = ask_one(&s)?;
        units.push(p);
        total += p;
        s.stock -= 1;
        s.recent_buys += 1;
    }
    Some((total, units))
}

/// Recent demand rolled forward to a new day: half of what was there plus
/// today's purchases.
pub fn roll_demand(recent: u128, today: u128) -> u128 {
    recent / 2 + today
}

#[cfg(test)]
mod tests {
    use super::*;

    fn shelf(stock: u128, target: u128) -> Shelf {
        Shelf {
            city: 60,
            stock,
            recent_buys: 0,
            levers: Levers {
                target,
                margin_bps: 1_000,
            },
        }
    }

    #[test]
    fn stock_factor_shape() {
        assert_eq!(stock_factor_bps(0, 20), 20_000);
        assert_eq!(stock_factor_bps(20, 20), 10_000);
        assert_eq!(stock_factor_bps(60, 20), 5_000);
        assert!(stock_factor_bps(1_000, 20) < 1_000);
    }

    #[test]
    fn demand_is_capped_at_half() {
        assert_eq!(demand_factor_bps(0), 10_000);
        assert_eq!(demand_factor_bps(4), 12_000);
        assert_eq!(demand_factor_bps(100), 15_000);
    }

    #[test]
    fn at_target_prices_sit_around_city_price() {
        // fair after one more unit: 60 * 2*20/41 = 58.5, bid at -10% = 52
        assert_eq!(bid_one(&shelf(20, 20)), 52);
        // fair after one fewer unit: 60 * 40/39 = 61.5, ask at +10% = 68
        assert_eq!(ask_one(&shelf(20, 20)), Some(68));
    }

    #[test]
    fn flooding_a_stall_makes_each_unit_cheaper() {
        let (total, units) = quote_shop_buys(&shelf(20, 20), 10);
        assert_eq!(units.len(), 10);
        for w in units.windows(2) {
            assert!(
                w[1] <= w[0],
                "prices must not rise while selling: {units:?}"
            );
        }
        assert!(units[9] < units[0]);
        assert_eq!(total, units.iter().sum::<u128>());
    }

    #[test]
    fn buying_a_lot_gets_dearer() {
        let (_, units) = quote_shop_sells(&shelf(20, 20), 10).unwrap();
        for w in units.windows(2) {
            assert!(w[1] >= w[0], "prices must not fall while buying: {units:?}");
        }
    }

    #[test]
    fn cannot_buy_more_than_stock() {
        assert!(quote_shop_sells(&shelf(3, 20), 4).is_none());
        assert_eq!(ask_one(&shelf(0, 20)), None);
    }

    #[test]
    fn empty_stall_pays_up_to_double() {
        // fair after one unit with target 20: 60 * 40/21 = 114, minus 10%
        assert_eq!(bid_one(&shelf(0, 20)), 102);
    }

    #[test]
    fn ask_is_always_above_bid() {
        for stock in 1..200 {
            for target in [1, 5, 20, 80] {
                let s = shelf(stock, target);
                assert!(
                    ask_one(&s).unwrap() > bid_one(&s),
                    "stock {stock} target {target}"
                );
            }
        }
    }

    #[test]
    fn city_cart_spread() {
        assert_eq!(city_price("mango", Season::Garmi), 48);
        assert_eq!(city_bid("mango", Season::Garmi), 38);
        assert_eq!(city_ask("mango", Season::Garmi), 60);
        assert_eq!(city_price("pickle", Season::Tyohar), 600);
    }

    #[test]
    fn demand_halves_each_day() {
        assert_eq!(roll_demand(10, 0), 5);
        assert_eq!(roll_demand(10, 3), 8);
    }
}
