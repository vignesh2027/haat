//! NEP-297 event logs: the standard NEP-245 events and the game's own events.

use near_sdk::serde::Serialize;
use near_sdk::serde_json::{json, Value};
use near_sdk::{env, AccountId};

fn emit(standard: &str, event: &str, data: Value) {
    let body = json!({
        "standard": standard,
        "version": "1.0.0",
        "event": event,
        "data": data,
    });
    env::log_str(&format!("EVENT_JSON:{body}"));
}

fn strings(amounts: &[u128]) -> Vec<String> {
    amounts.iter().map(|a| a.to_string()).collect()
}

pub fn mt_mint(owner_id: &AccountId, token_ids: &[String], amounts: &[u128], memo: Option<&str>) {
    let mut log = json!({ "owner_id": owner_id, "token_ids": token_ids, "amounts": strings(amounts) });
    if let Some(m) = memo {
        log["memo"] = json!(m);
    }
    emit("nep245", "mt_mint", json!([log]));
}

pub fn mt_burn(owner_id: &AccountId, token_ids: &[String], amounts: &[u128], memo: Option<&str>) {
    let mut log = json!({ "owner_id": owner_id, "token_ids": token_ids, "amounts": strings(amounts) });
    if let Some(m) = memo {
        log["memo"] = json!(m);
    }
    emit("nep245", "mt_burn", json!([log]));
}

pub fn mt_transfer(
    old_owner_id: &AccountId,
    new_owner_id: &AccountId,
    token_ids: &[String],
    amounts: &[u128],
    memo: Option<&str>,
) {
    let mut log = json!({
        "old_owner_id": old_owner_id,
        "new_owner_id": new_owner_id,
        "token_ids": token_ids,
        "amounts": strings(amounts),
    });
    if let Some(m) = memo {
        log["memo"] = json!(m);
    }
    emit("nep245", "mt_transfer", json!([log]));
}

/// Game events, under the "haat" standard.
pub fn haat<T: Serialize>(event: &str, data: T) {
    emit("haat", event, json!([data]));
}
