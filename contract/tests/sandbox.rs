//! Integration tests: build the wasm, deploy it to a local near-sandbox and
//! play through real transactions.

use near_api::{AccountId, NearGas, NearToken, NetworkConfig, Signer};
use near_sdk::serde_json::{Value, json};
use std::sync::Arc;

struct World {
    network: NetworkConfig,
    signer: Arc<Signer>,
    market: near_api::Contract,
    owner: AccountId,
    meena: AccountId,
    kavya: AccountId,
    _sandbox: near_sandbox::Sandbox,
}

async fn account(sandbox: &near_sandbox::Sandbox, name: &str) -> testresult::TestResult<AccountId> {
    let id: AccountId = name.parse()?;
    sandbox
        .create_account(id.clone())
        .initial_balance(NearToken::from_near(20))
        .send()
        .await?;
    Ok(id)
}

async fn setup() -> testresult::TestResult<World> {
    let wasm = std::fs::read(cargo_near_build::build_with_cli(Default::default())?)?;
    let sandbox = near_sandbox::Sandbox::start_sandbox().await?;
    let network = NetworkConfig::from_rpc_url("sandbox", sandbox.rpc_addr.parse()?);
    let signer = Signer::from_secret_key(
        near_sandbox::config::DEFAULT_GENESIS_ACCOUNT_PRIVATE_KEY.parse()?,
    )?;

    let owner = account(&sandbox, "owner.sandbox").await?;
    let meena = account(&sandbox, "meena.sandbox").await?;
    let kavya = account(&sandbox, "kavya.sandbox").await?;
    let market_id = account(&sandbox, "market.sandbox").await?;

    near_api::Contract::deploy(market_id.clone())
        .use_code(wasm)
        .with_init_call("new", json!({ "owner_id": owner, "world_seed": 7 }))?
        .with_signer(signer.clone())
        .send_to(&network)
        .await?
        .assert_success();
    let market = near_api::Contract(market_id);

    let w = World {
        network,
        signer,
        market,
        owner: owner.clone(),
        meena: meena.clone(),
        kavya: kavya.clone(),
        _sandbox: sandbox,
    };
    w.call(
        &owner,
        "add_merchant",
        json!({
            "account_id": meena, "name": "Meena Didi", "items": ["chilli", "pickle"],
            "opening_kauri": "1500", "opening_stock": ["15", "3"],
        }),
        0,
    )
    .await?
    .assert_success();
    w.call(&owner, "register_player", json!({ "account_id": kavya }), 0)
        .await?
        .assert_success();
    Ok(w)
}

impl World {
    async fn call(
        &self,
        who: &AccountId,
        method: &str,
        args: Value,
        yocto: u128,
    ) -> testresult::TestResult<near_api::types::transaction::result::TransactionResult> {
        Ok(self
            .market
            .call_function(method, args)
            .transaction()
            .gas(NearGas::from_tgas(150))
            .deposit(NearToken::from_yoctonear(yocto))
            .with_signer(who.clone(), self.signer.clone())
            .send_to(&self.network)
            .await?)
    }

    async fn view(&self, method: &str, args: Value) -> testresult::TestResult<Value> {
        Ok(self
            .market
            .call_function(method, args)
            .read_only()
            .fetch_from(&self.network)
            .await?
            .data)
    }

    async fn balance(&self, who: &AccountId, token: &str) -> testresult::TestResult<u128> {
        let v = self
            .view(
                "mt_balance_of",
                json!({ "account_id": who, "token_id": token }),
            )
            .await?;
        Ok(v.as_str().unwrap().parse()?)
    }
}

#[tokio::test]
async fn sandbox_end_to_end() -> testresult::TestResult<()> {
    let w = setup().await?;

    // A player gathers and sells in one call, as the game does through its relayer.
    assert_eq!(w.balance(&w.kavya, "kauri").await?, 400);
    let res = w
        .call(
            &w.kavya,
            "play",
            json!({ "moves": [
                { "gather": { "place": "garden" } },
                { "gather": { "place": "garden" } },
            ]}),
            0,
        )
        .await?;
    res.assert_success();
    let chilli = w.balance(&w.kavya, "chilli").await?;
    assert!(chilli >= 4, "gathered {chilli}");

    let quote = w
        .view(
            "quote",
            json!({ "merchant_id": w.meena, "token_id": "chilli", "side": "sell", "amount": "4" }),
        )
        .await?;
    let total: u128 = quote["total"].as_str().unwrap().parse()?;
    w.call(&w.kavya, "play", json!({ "moves": [
        { "sell": { "merchant_id": w.meena, "token_id": "chilli", "amount": "4", "min_total": total.to_string() } },
    ]}), 0)
    .await?
    .assert_success();
    assert_eq!(w.balance(&w.kavya, "kauri").await?, 400 + total);
    assert_eq!(w.balance(&w.meena, "chilli").await?, 19);

    // Prices are on chain and visible to anyone.
    let shops = w.view("get_merchants", json!({})).await?;
    assert_eq!(shops[0]["name"], "Meena Didi");
    assert_eq!(shops[0]["open"], true);

    // Only the shop itself can change its levers.
    w.call(
        &w.kavya,
        "set_policies",
        json!({ "policies": [], "note": "x" }),
        0,
    )
    .await?
    .assert_failure();
    w.call(
        &w.meena,
        "set_policies",
        json!({
            "policies": [{ "token_id": "chilli", "target_stock": 40, "margin_bps": 600 }],
            "note": "More chillies please",
        }),
        0,
    )
    .await?
    .assert_success();
    let shop = w
        .view("get_merchant", json!({ "account_id": w.meena }))
        .await?;
    assert_eq!(shop["note"], "More chillies please");

    // The owner moves the calendar on and a snapshot is recorded.
    w.call(&w.owner, "advance_day", json!({}), 0)
        .await?
        .assert_success();
    let history = w
        .view("history", json!({ "from_day": 0, "limit": 10 }))
        .await?;
    assert_eq!(history.as_array().unwrap().len(), 1);
    Ok(())
}

#[tokio::test]
async fn sandbox_nep245_transfers() -> testresult::TestResult<()> {
    let w = setup().await?;

    // Standard transfers need exactly one yoctoNEAR.
    w.call(
        &w.kavya,
        "mt_transfer",
        json!({ "receiver_id": w.owner, "token_id": "kauri", "amount": "5" }),
        0,
    )
    .await?
    .assert_failure();
    w.call(
        &w.kavya,
        "mt_transfer",
        json!({ "receiver_id": w.owner, "token_id": "kauri", "amount": "5" }),
        1,
    )
    .await?
    .assert_success();
    assert_eq!(w.balance(&w.owner, "kauri").await?, 5);

    // transfer_call to an account with no contract: the receiver call fails and
    // mt_resolve_transfer gives the tokens back.
    let _outcome = w
        .call(
            &w.kavya,
            "mt_transfer_call",
            json!({
                "receiver_id": w.meena, "token_id": "kauri", "amount": "50", "msg": "",
            }),
            1,
        )
        .await?;
    assert_eq!(w.balance(&w.kavya, "kauri").await?, 395);
    assert_eq!(w.balance(&w.meena, "kauri").await?, 1500);

    let supply = w.view("mt_supply", json!({ "token_id": "kauri" })).await?;
    assert_eq!(supply, json!("1900"));
    Ok(())
}
