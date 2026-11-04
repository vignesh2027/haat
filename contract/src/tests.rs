use super::*;
use near_sdk::test_utils::{VMContextBuilder, get_logs};
use near_sdk::{NearToken, testing_env};

fn acc(s: &str) -> AccountId {
    s.parse().unwrap()
}

fn as_caller(who: &str) {
    testing_env!(
        VMContextBuilder::new()
            .current_account_id(acc("market.test"))
            .predecessor_account_id(acc(who))
            .build()
    );
}

fn register_outside(m: &mut Market, who: &str) {
    testing_env!(
        VMContextBuilder::new()
            .current_account_id(acc("market.test"))
            .predecessor_account_id(acc(who))
            .attached_deposit(STORAGE_DEPOSIT)
            .build()
    );
    assert!(m.storage_deposit(None));
}

fn as_caller_with_yocto(who: &str) {
    testing_env!(
        VMContextBuilder::new()
            .current_account_id(acc("market.test"))
            .predecessor_account_id(acc(who))
            .attached_deposit(NearToken::from_yoctonear(1))
            .build()
    );
}

/// A market with one shop (meena: chilli and pickle) and one player (kavya).
fn setup() -> Market {
    as_caller("owner.test");
    let mut m = Market::new(acc("owner.test"), 7);
    m.add_merchant(
        acc("meena.test"),
        "Meena Didi".into(),
        vec!["chilli".into(), "pickle".into()],
        U128(1_000),
        vec![U128(10), U128(2)],
        None,
    );
    m.register_player(acc("kavya.test"));
    m
}

fn bal(m: &Market, token: &str, who: &str) -> u128 {
    m.mt_balance_of(acc(who), token.into()).0
}

/// Gathers chilli until the player has at least `n`.
fn gather_chilli(m: &mut Market, who: &str, n: u128) {
    as_caller(who);
    while bal(m, "chilli", who) < n {
        m.gather("garden".into());
    }
}

#[test]
fn new_player_gets_starter_purse_once() {
    let mut m = setup();
    assert_eq!(bal(&m, KAURI, "kavya.test"), STARTER_PURSE);
    as_caller("owner.test");
    assert!(!m.register_player(acc("kavya.test")));
    assert_eq!(bal(&m, KAURI, "kavya.test"), STARTER_PURSE);
    assert_eq!(m.get_world().players, 1);
}

#[test]
#[should_panic(expected = "only the owner")]
fn only_owner_registers_players() {
    let mut m = setup();
    as_caller("kavya.test");
    m.register_player(acc("other.test"));
}

#[test]
fn gathering_uses_energy_and_follows_season() {
    let mut m = setup();
    as_caller("kavya.test");
    let got = m.gather("grove".into()).0;
    assert!(got == 3 || got == 4, "summer mango yield was {got}");
    assert_eq!(
        m.get_player(acc("kavya.test")).energy_left,
        ENERGY_PER_DAY - 1
    );
    let logs = get_logs();
    assert!(
        logs.iter()
            .any(|l| l.contains("\"standard\":\"nep245\"") && l.contains("mt_mint"))
    );
}

#[test]
#[should_panic(expected = "no energy left")]
fn energy_runs_out() {
    let mut m = setup();
    as_caller("kavya.test");
    for _ in 0..=ENERGY_PER_DAY {
        m.gather("river".into());
    }
}

#[test]
fn energy_refills_next_day() {
    let mut m = setup();
    as_caller("kavya.test");
    for _ in 0..ENERGY_PER_DAY {
        m.gather("river".into());
    }
    as_caller("owner.test");
    m.advance_day();
    as_caller("kavya.test");
    m.gather("river".into());
    assert_eq!(
        m.get_player(acc("kavya.test")).energy_left,
        ENERGY_PER_DAY - 1
    );
}

#[test]
#[should_panic(expected = "not a registered player")]
fn strangers_cannot_gather() {
    let mut m = setup();
    as_caller("stranger.test");
    m.gather("grove".into());
}

#[test]
fn craft_pickle() {
    let mut m = setup();
    as_caller("kavya.test");
    while bal(&m, "mango", "kavya.test") < 4 {
        m.gather("grove".into());
    }
    gather_chilli(&mut m, "kavya.test", 2);
    let mango = bal(&m, "mango", "kavya.test");
    let chilli = bal(&m, "chilli", "kavya.test");
    as_caller("kavya.test");
    m.craft("pickle".into(), U128(2));
    assert_eq!(bal(&m, "pickle", "kavya.test"), 2);
    assert_eq!(bal(&m, "mango", "kavya.test"), mango - 4);
    assert_eq!(bal(&m, "chilli", "kavya.test"), chilli - 2);
}

#[test]
fn selling_moves_goods_and_pays_the_quote() {
    let mut m = setup();
    gather_chilli(&mut m, "kavya.test", 4);
    let q = m.quote(acc("meena.test"), "chilli".into(), "sell".into(), U128(4));
    let before = bal(&m, KAURI, "kavya.test");
    as_caller("kavya.test");
    let paid = m
        .sell(acc("meena.test"), "chilli".into(), U128(4), U128(0))
        .0;
    assert_eq!(Some(U128(paid)), q.total);
    assert_eq!(bal(&m, KAURI, "kavya.test"), before + paid);
    assert_eq!(bal(&m, "chilli", "meena.test"), 14);
}

#[test]
fn flooding_a_stall_lowers_its_price() {
    let mut m = setup();
    let first = m.get_merchant(acc("meena.test")).unwrap().shelves[0].bid.0;
    gather_chilli(&mut m, "kavya.test", 12);
    as_caller("kavya.test");
    m.sell(acc("meena.test"), "chilli".into(), U128(12), U128(0));
    let after = m.get_merchant(acc("meena.test")).unwrap().shelves[0].bid.0;
    assert!(
        after < first,
        "bid should fall after flooding: {first} then {after}"
    );
}

#[test]
#[should_panic(expected = "price moved")]
fn sell_respects_min_total() {
    let mut m = setup();
    gather_chilli(&mut m, "kavya.test", 2);
    as_caller("kavya.test");
    m.sell(acc("meena.test"), "chilli".into(), U128(2), U128(1_000_000));
}

#[test]
fn buying_raises_demand_and_price() {
    let mut m = setup();
    let ask_before = m.get_merchant(acc("meena.test")).unwrap().shelves[0]
        .ask
        .unwrap()
        .0;
    as_caller("kavya.test");
    m.buy(acc("meena.test"), "chilli".into(), U128(3), U128(10_000));
    let shelf = &m.get_merchant(acc("meena.test")).unwrap().shelves[0];
    assert_eq!(shelf.recent_demand.0, 3);
    assert!(shelf.ask.unwrap().0 > ask_before);
    assert_eq!(bal(&m, "chilli", "kavya.test"), 3);
}

#[test]
fn demand_fades_by_half_each_day() {
    let mut m = setup();
    as_caller("kavya.test");
    m.buy(acc("meena.test"), "chilli".into(), U128(2), U128(10_000));
    as_caller("owner.test");
    m.advance_day();
    assert_eq!(
        m.get_merchant(acc("meena.test")).unwrap().shelves[0]
            .recent_demand
            .0,
        2
    );
    m.advance_day();
    assert_eq!(
        m.get_merchant(acc("meena.test")).unwrap().shelves[0]
            .recent_demand
            .0,
        1
    );
}

#[test]
#[should_panic(expected = "has only")]
fn cannot_buy_more_than_the_shelf() {
    let mut m = setup();
    as_caller("kavya.test");
    m.buy(acc("meena.test"), "pickle".into(), U128(3), U128(100_000));
}

#[test]
fn shopkeeper_sets_own_policy_and_note() {
    let mut m = setup();
    as_caller("meena.test");
    m.set_policies(
        vec![Policy {
            token_id: "chilli".into(),
            target_stock: 60,
            margin_bps: 600,
        }],
        "Chillies sell fast before the rains".into(),
    );
    let v = m.get_merchant(acc("meena.test")).unwrap();
    assert_eq!(v.shelves[0].target_stock, 60);
    assert_eq!(v.note, "Chillies sell fast before the rains");
}

#[test]
#[should_panic(expected = "no such shop")]
fn players_cannot_set_shop_policy() {
    let mut m = setup();
    as_caller("kavya.test");
    m.set_policies(vec![], "hi".into());
}

#[test]
#[should_panic(expected = "margin must stay between 200 and 4000")]
fn policy_bounds_are_enforced() {
    let mut m = setup();
    as_caller("meena.test");
    m.set_policies(
        vec![Policy {
            token_id: "chilli".into(),
            target_stock: 10,
            margin_bps: 9_000,
        }],
        "".into(),
    );
}

#[test]
fn city_cart_trades() {
    let mut m = setup();
    as_caller("meena.test");
    let got = m.city_sell("chilli".into(), U128(5)).0;
    assert_eq!(got, 5 * pricing::city_bid("chilli", world::Season::Garmi));
    assert_eq!(bal(&m, "chilli", "meena.test"), 5);
    let paid = m.city_buy("mango".into(), U128(2)).0;
    assert_eq!(paid, 2 * pricing::city_ask("mango", world::Season::Garmi));
    assert_eq!(bal(&m, "mango", "meena.test"), 2);
}

fn first_flood_day(seed: u64) -> u32 {
    (0..200).find(|d| world::is_flood(seed, *d)).unwrap()
}

#[test]
#[should_panic(expected = "road is flooded")]
fn city_cart_does_not_come_on_flood_days() {
    let mut m = setup();
    as_caller("owner.test");
    for _ in 0..first_flood_day(7) {
        m.advance_day();
    }
    assert!(m.get_world().flood);
    as_caller("meena.test");
    m.city_sell("chilli".into(), U128(1));
}

/// Drains Meena's purse below the closing line by selling to the city at a loss.
fn bankrupt_meena(m: &mut Market) {
    as_caller("meena.test");
    let price = pricing::city_ask("fish", world::Season::Garmi);
    let units = (bal(m, KAURI, "meena.test") - CLOSE_BELOW) / price + 1;
    m.city_buy("fish".into(), U128(units));
}

#[test]
fn a_broke_shop_closes_and_investment_reopens_it() {
    let mut m = setup();
    bankrupt_meena(&mut m);
    let v = m.get_merchant(acc("meena.test")).unwrap();
    assert!(!v.open, "shop should close with {} kauri", v.kauri.0);
    let kinds: Vec<String> = m.chronicle(0, 100).into_iter().map(|c| c.kind).collect();
    assert!(kinds.contains(&"shop_closed".to_string()));

    as_caller("kavya.test");
    let shares = m.invest(acc("meena.test"), U128(200)).0;
    assert!(shares > 0);
    let v = m.get_merchant(acc("meena.test")).unwrap();
    assert!(v.open);
    assert_eq!(
        bal(&m, &share_token(&acc("meena.test")), "kavya.test"),
        shares
    );
    let last = m.chronicle(0, 100).pop().unwrap();
    assert_eq!(last.kind, "shop_reopened");
    assert_eq!(last.account_id, Some(acc("kavya.test")));
}

#[test]
#[should_panic(expected = "closed until someone invests")]
fn closed_shops_do_not_trade() {
    let mut m = setup();
    bankrupt_meena(&mut m);
    gather_chilli(&mut m, "kavya.test", 1);
    as_caller("kavya.test");
    m.sell(acc("meena.test"), "chilli".into(), U128(1), U128(0));
}

#[test]
fn cheap_shops_sell_bigger_stakes() {
    let mut rich = setup();
    as_caller("kavya.test");
    let rich_shares = rich.invest(acc("meena.test"), U128(100)).0;

    let mut broke = setup();
    bankrupt_meena(&mut broke);
    as_caller("kavya.test");
    let broke_shares = broke.invest(acc("meena.test"), U128(100)).0;
    assert!(
        broke_shares > rich_shares,
        "{broke_shares} should beat {rich_shares}"
    );
}

#[test]
fn profits_pay_dividends_to_investors() {
    let mut m = setup();
    as_caller("kavya.test");
    m.invest(acc("meena.test"), U128(300));
    // Meena settles the opening day with no profit yet.
    as_caller("meena.test");
    assert_eq!(m.settle_day().0, 0);
    // Next day she sells stock to the city and makes money.
    as_caller("owner.test");
    m.advance_day();
    as_caller("meena.test");
    m.city_sell("chilli".into(), U128(10));
    let dividend = m.settle_day().0;
    assert!(dividend > 0);
    let p = m.get_player(acc("kavya.test"));
    assert_eq!(p.holdings.len(), 1);
    let pending = p.holdings[0].pending_dividends.0;
    assert!(pending > 0 && pending <= dividend);
    let before = bal(&m, KAURI, "kavya.test");
    as_caller("kavya.test");
    assert_eq!(m.claim_dividends(acc("meena.test")).0, pending);
    assert_eq!(bal(&m, KAURI, "kavya.test"), before + pending);
    assert_eq!(m.claim_dividends(acc("meena.test")).0, 0);
}

#[test]
#[should_panic(expected = "already settled today")]
fn settle_once_per_day() {
    let mut m = setup();
    as_caller("meena.test");
    m.settle_day();
    m.settle_day();
}

#[test]
fn transferred_shares_keep_earned_dividends_with_the_seller() {
    let mut m = setup();
    as_caller("owner.test");
    m.register_player(acc("arjun.test"));
    as_caller("kavya.test");
    let shares = m.invest(acc("meena.test"), U128(300)).0;
    as_caller("meena.test");
    m.settle_day();
    as_caller("owner.test");
    m.advance_day();
    as_caller("meena.test");
    m.city_sell("chilli".into(), U128(10));
    m.settle_day();
    let earned = m.get_player(acc("kavya.test")).holdings[0]
        .pending_dividends
        .0;
    assert!(earned > 0);

    as_caller_with_yocto("kavya.test");
    m.mt_transfer(
        acc("arjun.test"),
        share_token(&acc("meena.test")),
        U128(shares),
        None,
        None,
    );
    let k = m.get_player(acc("kavya.test"));
    assert_eq!(k.holdings[0].pending_dividends.0, earned);
    assert_eq!(k.holdings[0].shares.0, 0);
    let a = m.get_player(acc("arjun.test"));
    assert_eq!(a.holdings[0].pending_dividends.0, 0);
    assert_eq!(a.holdings[0].shares.0, shares);
}

#[test]
fn mt_transfer_moves_tokens_and_logs_nep245() {
    let mut m = setup();
    register_outside(&mut m, "friend.test");
    as_caller_with_yocto("kavya.test");
    m.mt_transfer(
        acc("friend.test"),
        KAURI.into(),
        U128(15),
        None,
        Some("gift".into()),
    );
    assert_eq!(bal(&m, KAURI, "friend.test"), 15);
    let log = get_logs()
        .into_iter()
        .find(|l| l.contains("mt_transfer"))
        .unwrap();
    assert!(log.starts_with("EVENT_JSON:"));
    assert!(log.contains("\"old_owner_id\":\"kavya.test\""));
    assert!(log.contains("\"amounts\":[\"15\"]"));
}

#[test]
#[should_panic(expected = "Requires attached deposit of exactly 1 yoctoNEAR")]
fn mt_transfer_needs_one_yocto() {
    let mut m = setup();
    as_caller("kavya.test");
    m.mt_transfer(acc("friend.test"), KAURI.into(), U128(1), None, None);
}

#[test]
#[should_panic(expected = "duplicate token id")]
fn batch_rejects_duplicates() {
    let mut m = setup();
    register_outside(&mut m, "friend.test");
    as_caller_with_yocto("kavya.test");
    m.mt_batch_transfer(
        acc("friend.test"),
        vec![KAURI.into(), KAURI.into()],
        vec![U128(1), U128(1)],
        None,
        None,
    );
}

#[test]
fn supply_tracks_mint_and_burn() {
    let mut m = setup();
    let kauri = m.mt_supply(KAURI.into()).unwrap().0;
    assert_eq!(kauri, 1_000 + STARTER_PURSE);
    as_caller("meena.test");
    let got = m.city_sell("chilli".into(), U128(2)).0;
    assert_eq!(m.mt_supply(KAURI.into()).unwrap().0, kauri + got);
    assert_eq!(m.mt_supply("chilli".into()).unwrap().0, 8);
    assert!(m.mt_token(vec!["mango".into(), "gold".into()])[1].is_none());
}

#[test]
fn advance_day_writes_history_and_chronicle() {
    let mut m = setup();
    as_caller("owner.test");
    for _ in 0..8 {
        m.advance_day();
    }
    let h = m.history(0, 100);
    assert_eq!(h.len(), 8);
    assert_eq!(h[0].shops.len(), 1);
    assert_eq!(h[0].shops[0].shelves.len(), 2);
    let kinds: Vec<String> = m.chronicle(0, 100).into_iter().map(|c| c.kind).collect();
    assert!(kinds.contains(&"season_barsaat".to_string()));
    assert_eq!(m.get_world().season, "barsaat");
    assert_eq!(m.get_world().forecast.len(), 3);
}

#[test]
fn play_runs_several_moves_in_one_call() {
    let mut m = setup();
    as_caller("kavya.test");
    let out = m.play(vec![
        Move::Gather {
            place: "garden".into(),
        },
        Move::Gather {
            place: "garden".into(),
        },
        Move::Sell {
            merchant_id: acc("meena.test"),
            token_id: "chilli".into(),
            amount: U128(2),
            min_total: U128(1),
        },
    ]);
    assert_eq!(out.len(), 3);
    assert!(out[2].0 > 0);
    assert_eq!(
        m.get_player(acc("kavya.test")).energy_left,
        ENERGY_PER_DAY - 2
    );
}

#[test]
fn play_moves_parse_from_json() {
    let moves: Vec<Move> = near_sdk::serde_json::from_str(
        r#"[{"gather":{"place":"grove"}},{"sell":{"merchant_id":"meena.test","token_id":"chilli","amount":"1","min_total":"0"}}]"#,
    )
    .unwrap();
    assert_eq!(moves.len(), 2);
}

/// Gives a player extra kauri from a rich helper shop, through a standard transfer.
fn fund(m: &mut Market, who: &str, amount: u128) {
    as_caller("owner.test");
    if m.get_merchant(acc("bank.test")).is_none() {
        m.add_merchant(
            acc("bank.test"),
            "Bank".into(),
            vec!["rice".into()],
            U128(1_000_000),
            vec![U128(0)],
            None,
        );
    }
    as_caller_with_yocto("bank.test");
    m.mt_transfer(acc(who), KAURI.into(), U128(amount), None, None);
}

// Review finding H1: buying raised the demand factor, which also raised the
// shop's bid, so buy-then-sell in one play() call made money.
#[test]
fn regression_buy_then_sell_back_never_profits() {
    let mut m = setup();
    as_caller("meena.test");
    m.set_policies(
        vec![Policy {
            token_id: "chilli".into(),
            target_stock: 40,
            margin_bps: 600,
        }],
        "thin".into(),
    );
    fund(&mut m, "kavya.test", 3_000);
    let before = bal(&m, KAURI, "kavya.test");
    as_caller("kavya.test");
    m.play(vec![
        Move::Buy {
            merchant_id: acc("meena.test"),
            token_id: "chilli".into(),
            amount: U128(10),
            max_total: U128(10_000),
        },
        Move::Sell {
            merchant_id: acc("meena.test"),
            token_id: "chilli".into(),
            amount: U128(10),
            min_total: U128(0),
        },
    ]);
    assert!(
        bal(&m, KAURI, "kavya.test") < before,
        "a round trip must cost money"
    );
}

#[test]
fn property_round_trips_lose_for_every_shelf() {
    use pricing::*;
    for city in [48u128, 60, 80, 112, 300, 600] {
        for target in [1u128, 4, 6, 15, 30, 40, 200] {
            for margin_bps in [200u128, 600, 1_000, 4_000] {
                for stock in [1u128, 5, 10, 20, 40, 80] {
                    for recent_buys in [0u128, 3, 10] {
                        for n in [1u128, 5, 10] {
                            let shelf = Shelf {
                                city,
                                stock,
                                recent_buys,
                                levers: Levers { target, margin_bps },
                            };
                            let Some((cost, _)) = quote_shop_sells(&shelf, n) else {
                                continue;
                            };
                            let after = Shelf {
                                stock: stock - n,
                                recent_buys: recent_buys + n,
                                ..shelf
                            };
                            let (back, _) = quote_shop_buys(&after, n);
                            assert!(
                                back < cost,
                                "profit: city {city} target {target} margin {margin_bps} stock {stock} n {n}"
                            );
                        }
                    }
                }
            }
        }
    }
}

// Review finding H2: a falling purse reset the dividend mark, so cash that only
// went out and came back was paid out again and again.
#[test]
fn regression_dividends_only_from_new_highs() {
    let mut m = setup();
    fund(&mut m, "kavya.test", 20_000);
    as_caller("kavya.test");
    m.invest(acc("meena.test"), U128(1_000));
    let purse_start = bal(&m, KAURI, "meena.test");
    let mut dividends = 0;
    for _ in 0..5 {
        as_caller("kavya.test");
        m.buy(acc("meena.test"), "chilli".into(), U128(8), U128(100_000));
        as_caller("meena.test");
        dividends += m.settle_day().0;
        as_caller("owner.test");
        m.advance_day();
        as_caller("kavya.test");
        m.sell(acc("meena.test"), "chilli".into(), U128(8), U128(0));
        as_caller("meena.test");
        dividends += m.settle_day().0;
        as_caller("owner.test");
        m.advance_day();
    }
    let purse_end = bal(&m, KAURI, "meena.test");
    let real_gain = (purse_end + dividends).saturating_sub(purse_start);
    assert!(
        dividends <= real_gain,
        "paid {dividends} in dividends on a real gain of {real_gain}"
    );
}

// Review finding M3: investing just before the evening settle took a share of
// profit made before the investment.
#[test]
fn regression_late_investors_do_not_share_earlier_profit() {
    let mut m = setup();
    as_caller("owner.test");
    m.register_player(acc("arjun.test"));
    as_caller("kavya.test");
    m.invest(acc("meena.test"), U128(300));
    as_caller("meena.test");
    m.city_sell("chilli".into(), U128(10)); // a good day, before arjun invests
    as_caller("arjun.test");
    m.invest(acc("meena.test"), U128(300));
    as_caller("meena.test");
    m.settle_day();
    let arjun = m.get_player(acc("arjun.test"));
    assert_eq!(
        arjun.holdings[0].pending_dividends.0, 0,
        "arjun was not there for that profit"
    );
    let kavya = m.get_player(acc("kavya.test"));
    assert!(kavya.holdings[0].pending_dividends.0 > 0);
}

// Review finding M2: the open flag only changed on game calls.
#[test]
fn regression_kauri_sent_to_a_closed_shop_reopens_it() {
    let mut m = setup();
    bankrupt_meena(&mut m);
    assert!(!m.get_merchant(acc("meena.test")).unwrap().open);
    as_caller_with_yocto("kavya.test");
    m.mt_transfer(acc("meena.test"), KAURI.into(), U128(300), None, None);
    assert!(m.get_merchant(acc("meena.test")).unwrap().open);
    let last = m.chronicle(0, 100).pop().unwrap();
    assert_eq!(last.kind, "shop_reopened");
}

// Review finding M4: transfers to arbitrary accounts made the contract pay storage.
#[test]
#[should_panic(expected = "receiver is not registered")]
fn transfers_to_unregistered_accounts_are_refused() {
    let mut m = setup();
    as_caller_with_yocto("kavya.test");
    m.mt_transfer(acc("stranger.test"), KAURI.into(), U128(1), None, None);
}

#[test]
#[should_panic(expected = "attach 0.01 NEAR")]
fn storage_deposit_needs_the_deposit() {
    let mut m = setup();
    as_caller("stranger.test");
    m.storage_deposit(None);
}

#[test]
fn history_keeps_the_last_sixty_days() {
    let mut m = setup();
    as_caller("owner.test");
    for _ in 0..70 {
        m.advance_day();
    }
    let h = m.history(0, 100);
    assert_eq!(h.len(), 60);
    assert_eq!(h[0].day, 10);
    assert_eq!(h[59].day, 69);
}

#[test]
fn metadata_names_every_token() {
    let m = setup();
    assert_eq!(m.mt_metadata_contract().spec, "mt-1.0.0");
    let all = m.mt_metadata_token_all(vec![
        KAURI.into(),
        "pickle".into(),
        share_token(&acc("meena.test")),
        "gold".into(),
    ]);
    assert_eq!(
        all[0].as_ref().unwrap().base.symbol.as_deref(),
        Some("KAURI")
    );
    assert!(
        all[1]
            .as_ref()
            .unwrap()
            .token
            .description
            .as_ref()
            .unwrap()
            .contains("Diwali")
    );
    assert!(all[2].as_ref().unwrap().base.name.starts_with("Shares in"));
    assert!(all[3].is_none());
}

#[test]
fn leaderboard_counts_rescues_and_net_worth() {
    let mut m = setup();
    bankrupt_meena(&mut m);
    as_caller("kavya.test");
    m.invest(acc("meena.test"), U128(200));
    let rows = m.leaderboard(0, 10);
    assert_eq!(rows.len(), 1);
    assert_eq!(rows[0].rescues, 1);
    assert!(rows[0].net_worth.0 >= rows[0].kauri.0);
}

/// A prudent shop with a reserve, like Bashir.
fn add_prudent(m: &mut Market) {
    as_caller("owner.test");
    m.add_merchant(
        acc("bashir.test"),
        "Bashir Bhai".into(),
        vec!["fish".into()],
        U128(1_000),
        vec![U128(5)],
        Some(Bounds { margin_min: 1_000, margin_max: 2_500, target_max: 25, cash_floor: U128(700) }),
    );
}

#[test]
#[should_panic(expected = "margin must stay between 1000 and 2500")]
fn shops_cannot_leave_their_character_margin() {
    let mut m = setup();
    add_prudent(&mut m);
    as_caller("bashir.test");
    m.set_policies(vec![Policy { token_id: "fish".into(), target_stock: 10, margin_bps: 600 }], "thin".into());
}

#[test]
#[should_panic(expected = "target stock must stay at or below 25")]
fn shops_cannot_leave_their_character_target() {
    let mut m = setup();
    add_prudent(&mut m);
    as_caller("bashir.test");
    m.set_policies(vec![Policy { token_id: "fish".into(), target_stock: 90, margin_bps: 1_200 }], "big".into());
}

#[test]
fn a_reserve_keeps_a_prudent_shop_open() {
    let mut m = setup();
    add_prudent(&mut m);
    // Players cannot drain it below its reserve.
    as_caller("kavya.test");
    for _ in 0..ENERGY_PER_DAY {
        m.gather("river".into());
    }
    let fish = bal(&m, "fish", "kavya.test");
    let q = m.quote(acc("bashir.test"), "fish".into(), "sell".into(), U128(fish));
    assert_eq!(q.spendable.0, 300);
    let mut sold = 0;
    for n in (1..=fish).rev() {
        let q = m.quote(acc("bashir.test"), "fish".into(), "sell".into(), U128(n));
        if q.total.unwrap().0 <= q.spendable.0 {
            as_caller("kavya.test");
            m.sell(acc("bashir.test"), "fish".into(), U128(n), U128(0));
            sold = n;
            break;
        }
    }
    assert!(sold > 0);
    assert!(bal(&m, KAURI, "bashir.test") >= 700);
    assert!(m.get_merchant(acc("bashir.test")).unwrap().open);
}

#[test]
#[should_panic(expected = "keeps 700 kauri in reserve")]
fn a_reserve_limits_city_buying() {
    let mut m = setup();
    add_prudent(&mut m);
    as_caller("bashir.test");
    m.city_buy("fish".into(), U128(10)); // about 880 kauri
}
