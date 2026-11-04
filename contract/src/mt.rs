//! NEP-245 multi token core: balances, supply, transfers and the standard methods.
//!
//! Every item, the kauri currency and each shop's shares are tokens in this one
//! contract. Game actions move tokens through the same internal functions, so
//! every gather, trade and dividend shows up as a standard NEP-245 event.

use crate::*;
use near_sdk::json_types::U128;
use near_sdk::{Gas, PromiseOrValue, assert_one_yocto, ext_contract};

/// Token info returned by `mt_token`.
#[near(serializers = [json])]
pub struct Token {
    pub token_id: String,
    pub owner_id: Option<AccountId>,
}

#[allow(dead_code)]
#[ext_contract(ext_mt_receiver)]
pub trait MultiTokenReceiver {
    fn mt_on_transfer(
        &mut self,
        sender_id: AccountId,
        previous_owner_ids: Vec<AccountId>,
        token_ids: Vec<String>,
        amounts: Vec<U128>,
        msg: String,
    ) -> PromiseOrValue<Vec<U128>>;
}

/// Gas for `mt_resolve_transfer`, which grows with the number of tokens to refund.
fn gas_for_resolve(tokens: usize) -> Gas {
    Gas::from_tgas(5 + 3 * tokens as u64)
}
const GAS_FOR_RECEIVER: Gas = Gas::from_tgas(30);

impl Market {
    pub(crate) fn balance(&self, token_id: &str, account_id: &AccountId) -> u128 {
        self.balances
            .get(&(token_id.to_string(), account_id.clone()))
            .copied()
            .unwrap_or(0)
    }

    fn set_balance(&mut self, token_id: &str, account_id: &AccountId, amount: u128) {
        let key = (token_id.to_string(), account_id.clone());
        if amount == 0 {
            self.balances.remove(&key);
        } else {
            self.balances.insert(key, amount);
        }
    }

    fn add_supply(&mut self, token_id: &str, delta: u128, add: bool) {
        let cur = self.supply.get(token_id).copied().unwrap_or(0);
        let next = if add {
            cur + delta
        } else {
            cur.checked_sub(delta).expect("supply underflow")
        };
        self.supply.insert(token_id.to_string(), next);
    }

    /// Settles a shareholder's dividends before their share balance changes.
    fn before_share_change(&mut self, token_id: &str, account_id: &AccountId) {
        if let Some(merchant) = token_id.strip_prefix(SHARE_PREFIX) {
            if let Ok(merchant_id) = merchant.parse::<AccountId>() {
                if account_id != &env::current_account_id() {
                    self.settle_holder(&merchant_id, account_id);
                }
            }
        }
    }

    pub(crate) fn internal_mint(
        &mut self,
        token_id: &str,
        to: &AccountId,
        amount: u128,
        memo: Option<&str>,
    ) {
        if amount == 0 {
            return;
        }
        self.before_share_change(token_id, to);
        let bal = self.balance(token_id, to);
        self.set_balance(token_id, to, bal + amount);
        self.add_supply(token_id, amount, true);
        events::mt_mint(to, &[token_id.to_string()], &[amount], memo);
    }

    pub(crate) fn internal_burn(
        &mut self,
        token_id: &str,
        from: &AccountId,
        amount: u128,
        memo: Option<&str>,
    ) {
        if amount == 0 {
            return;
        }
        let bal = self.balance(token_id, from);
        require!(
            bal >= amount,
            format!("not enough {token_id}: have {bal}, need {amount}")
        );
        self.before_share_change(token_id, from);
        self.set_balance(token_id, from, bal - amount);
        self.add_supply(token_id, amount, false);
        events::mt_burn(from, &[token_id.to_string()], &[amount], memo);
    }

    /// Moves tokens without emitting an event (callers emit one event per batch).
    fn move_tokens(&mut self, token_id: &str, from: &AccountId, to: &AccountId, amount: u128) {
        require!(amount > 0, "amount must be positive");
        require!(from != to, "sender and receiver must differ");
        let bal = self.balance(token_id, from);
        require!(
            bal >= amount,
            format!("not enough {token_id}: have {bal}, need {amount}")
        );
        self.before_share_change(token_id, from);
        self.before_share_change(token_id, to);
        self.set_balance(token_id, from, bal - amount);
        let to_bal = self.balance(token_id, to);
        self.set_balance(token_id, to, to_bal + amount);
    }

    pub(crate) fn internal_transfer(
        &mut self,
        token_id: &str,
        from: &AccountId,
        to: &AccountId,
        amount: u128,
        memo: Option<&str>,
    ) {
        self.move_tokens(token_id, from, to, amount);
        events::mt_transfer(from, to, &[token_id.to_string()], &[amount], memo);
    }

    fn internal_batch_transfer(
        &mut self,
        from: &AccountId,
        to: &AccountId,
        token_ids: &[String],
        amounts: &[U128],
        approvals: Option<Vec<Option<(AccountId, u64)>>>,
        memo: Option<&str>,
    ) {
        require!(!token_ids.is_empty(), "no tokens given");
        require!(
            token_ids.len() == amounts.len(),
            "token_ids and amounts must have the same length"
        );
        if let Some(a) = approvals {
            require!(a.iter().all(Option::is_none), "approvals are not supported");
        }
        for (i, t) in token_ids.iter().enumerate() {
            require!(!token_ids[..i].contains(t), "duplicate token id");
        }
        require!(
            self.can_receive(to),
            "receiver is not registered with the market: call storage_deposit first"
        );
        let raw: Vec<u128> = amounts.iter().map(|a| a.0).collect();
        for (t, a) in token_ids.iter().zip(&raw) {
            self.move_tokens(t, from, to, *a);
        }
        events::mt_transfer(from, to, token_ids, &raw, memo);
        self.refresh_shops_after(token_ids, from, to);
    }

    /// A standard transfer of kauri can open or close a shop.
    fn refresh_shops_after(&mut self, token_ids: &[String], a: &AccountId, b: &AccountId) {
        if token_ids.iter().any(|t| t == KAURI) {
            for id in [a, b] {
                if self.merchants.contains_key(id) {
                    self.refresh_open(id, None);
                }
            }
        }
    }
}

#[near]
impl Market {
    #[payable]
    pub fn mt_transfer(
        &mut self,
        receiver_id: AccountId,
        token_id: String,
        amount: U128,
        approval: Option<(AccountId, u64)>,
        memo: Option<String>,
    ) {
        assert_one_yocto();
        let sender = env::predecessor_account_id();
        self.internal_batch_transfer(
            &sender,
            &receiver_id,
            &[token_id],
            &[amount],
            Some(vec![approval]),
            memo.as_deref(),
        );
    }

    #[payable]
    pub fn mt_batch_transfer(
        &mut self,
        receiver_id: AccountId,
        token_ids: Vec<String>,
        amounts: Vec<U128>,
        approvals: Option<Vec<Option<(AccountId, u64)>>>,
        memo: Option<String>,
    ) {
        assert_one_yocto();
        let sender = env::predecessor_account_id();
        self.internal_batch_transfer(
            &sender,
            &receiver_id,
            &token_ids,
            &amounts,
            approvals,
            memo.as_deref(),
        );
    }

    #[payable]
    pub fn mt_transfer_call(
        &mut self,
        receiver_id: AccountId,
        token_id: String,
        amount: U128,
        approval: Option<(AccountId, u64)>,
        memo: Option<String>,
        msg: String,
    ) -> PromiseOrValue<Vec<U128>> {
        self.mt_batch_transfer_call(
            receiver_id,
            vec![token_id],
            vec![amount],
            Some(vec![approval]),
            memo,
            msg,
        )
    }

    #[payable]
    pub fn mt_batch_transfer_call(
        &mut self,
        receiver_id: AccountId,
        token_ids: Vec<String>,
        amounts: Vec<U128>,
        approvals: Option<Vec<Option<(AccountId, u64)>>>,
        memo: Option<String>,
        msg: String,
    ) -> PromiseOrValue<Vec<U128>> {
        assert_one_yocto();
        require!(
            env::prepaid_gas()
                >= GAS_FOR_RECEIVER
                    .saturating_add(gas_for_resolve(token_ids.len()))
                    .saturating_add(Gas::from_tgas(10)),
            "attach more gas: 45 TGas plus 3 TGas per token"
        );
        let sender = env::predecessor_account_id();
        self.internal_batch_transfer(
            &sender,
            &receiver_id,
            &token_ids,
            &amounts,
            approvals,
            memo.as_deref(),
        );
        ext_mt_receiver::ext(receiver_id.clone())
            .with_static_gas(GAS_FOR_RECEIVER)
            .mt_on_transfer(
                sender.clone(),
                vec![sender.clone(); token_ids.len()],
                token_ids.clone(),
                amounts.clone(),
                msg,
            )
            .then(
                Self::ext(env::current_account_id())
                    .with_static_gas(gas_for_resolve(token_ids.len()))
                    .mt_resolve_transfer(sender, receiver_id, token_ids, amounts),
            )
            .into()
    }

    /// Returns unused tokens to the sender after `mt_transfer_call`. Returns the used amounts.
    /// The standard allows a different signature for this private callback;
    /// this one takes the single sender instead of per-token previous owners.
    #[private]
    pub fn mt_resolve_transfer(
        &mut self,
        sender_id: AccountId,
        receiver_id: AccountId,
        token_ids: Vec<String>,
        amounts: Vec<U128>,
    ) -> Vec<U128> {
        let unused: Vec<u128> = match env::promise_result_checked(0, 16 * 1024) {
            Ok(bytes) => near_sdk::serde_json::from_slice::<Vec<U128>>(&bytes)
                .ok()
                .filter(|v| v.len() == amounts.len())
                .map(|v| v.iter().zip(&amounts).map(|(u, a)| u.0.min(a.0)).collect())
                .unwrap_or_else(|| amounts.iter().map(|a| a.0).collect()),
            _ => amounts.iter().map(|a| a.0).collect(),
        };
        let mut used = Vec::with_capacity(amounts.len());
        for ((token_id, amount), want_back) in token_ids.iter().zip(&amounts).zip(unused) {
            let refund = want_back.min(self.balance(token_id, &receiver_id));
            if refund > 0 {
                self.internal_transfer(token_id, &receiver_id, &sender_id, refund, Some("refund"));
            }
            used.push(U128(amount.0 - refund));
        }
        self.refresh_shops_after(&token_ids, &sender_id, &receiver_id);
        used
    }

    pub fn mt_balance_of(&self, account_id: AccountId, token_id: String) -> U128 {
        U128(self.balance(&token_id, &account_id))
    }

    pub fn mt_batch_balance_of(&self, account_id: AccountId, token_ids: Vec<String>) -> Vec<U128> {
        token_ids
            .iter()
            .map(|t| U128(self.balance(t, &account_id)))
            .collect()
    }

    pub fn mt_supply(&self, token_id: String) -> Option<U128> {
        self.supply.get(&token_id).map(|s| U128(*s))
    }

    pub fn mt_batch_supply(&self, token_ids: Vec<String>) -> Vec<Option<U128>> {
        token_ids
            .iter()
            .map(|t| self.supply.get(t).map(|s| U128(*s)))
            .collect()
    }

    pub fn mt_token(&self, token_ids: Vec<String>) -> Vec<Option<Token>> {
        token_ids
            .into_iter()
            .map(|t| {
                self.supply.get(&t).map(|_| Token {
                    token_id: t.clone(),
                    owner_id: None,
                })
            })
            .collect()
    }

    // ---------- NEP-245 metadata extension ----------

    pub fn mt_metadata_contract(&self) -> MtContractMetadata {
        MtContractMetadata {
            spec: "mt-1.0.0".into(),
            name: "Haat village market".into(),
        }
    }

    pub fn mt_metadata_base_by_token_id(
        &self,
        token_ids: Vec<String>,
    ) -> Vec<Option<MtBaseTokenMetadata>> {
        token_ids.iter().map(|t| self.base_metadata(t)).collect()
    }

    pub fn mt_metadata_base_by_metadata_id(
        &self,
        base_metadata_ids: Vec<String>,
    ) -> Vec<Option<MtBaseTokenMetadata>> {
        self.mt_metadata_base_by_token_id(base_metadata_ids)
    }

    pub fn mt_metadata_token_by_token_id(
        &self,
        token_ids: Vec<String>,
    ) -> Vec<Option<MtTokenMetadata>> {
        token_ids.iter().map(|t| self.token_metadata(t)).collect()
    }

    pub fn mt_metadata_token_all(&self, token_ids: Vec<String>) -> Vec<Option<MtTokenMetadataAll>> {
        token_ids
            .iter()
            .map(|t| {
                Some(MtTokenMetadataAll {
                    base: self.base_metadata(t)?,
                    token: self.token_metadata(t)?,
                })
            })
            .collect()
    }
}

#[near(serializers = [json])]
pub struct MtContractMetadata {
    pub spec: String,
    pub name: String,
}

#[near(serializers = [json])]
pub struct MtBaseTokenMetadata {
    pub name: String,
    pub id: String,
    pub symbol: Option<String>,
    pub icon: Option<String>,
    pub decimals: Option<String>,
    pub base_uri: Option<String>,
    pub reference: Option<String>,
    pub copies: Option<u64>,
    pub reference_hash: Option<String>,
}

#[near(serializers = [json])]
pub struct MtTokenMetadata {
    pub title: Option<String>,
    pub description: Option<String>,
    pub media: Option<String>,
    pub media_hash: Option<String>,
    pub issued_at: Option<String>,
    pub expires_at: Option<String>,
    pub starts_at: Option<String>,
    pub updated_at: Option<String>,
    pub extra: Option<String>,
    pub reference: Option<String>,
    pub reference_hash: Option<String>,
}

#[near(serializers = [json])]
pub struct MtTokenMetadataAll {
    pub base: MtBaseTokenMetadata,
    pub token: MtTokenMetadata,
}

/// Name, symbol, emoji and description of each token the market issues.
fn describe(token_id: &str) -> Option<(String, String, &'static str, String)> {
    let (name, symbol, emoji, about) = match token_id {
        KAURI => (
            "Kauri",
            "KAURI",
            "🐚",
            "Cowrie shells, the money of the haat.",
        ),
        "mango" => (
            "Mango",
            "MANGO",
            "🥭",
            "Aam from the grove. Plentiful in summer.",
        ),
        "rice" => (
            "Rice",
            "RICE",
            "🌾",
            "Chawal from the paddy field. Harvested in the festival season.",
        ),
        "fish" => (
            "Fish",
            "FISH",
            "🐟",
            "Machli from the river ghat. Plenty in the monsoon.",
        ),
        "chilli" => (
            "Chilli",
            "CHILLI",
            "🌶️",
            "Mirchi from the garden. Scarce in the rains.",
        ),
        "pickle" => (
            "Pickle",
            "PICKLE",
            "🫙",
            "Achaar: two mangoes and a chilli. The city pays double near Diwali.",
        ),
        _ => {
            let shop = token_id.strip_prefix(SHARE_PREFIX)?;
            return Some((
                format!("Shares in {shop}"),
                "SHARE".into(),
                "🤝",
                format!("A share of the profits of the shop run by {shop}."),
            ));
        }
    };
    Some((name.into(), symbol.into(), emoji, about.into()))
}

fn emoji_icon(emoji: &str) -> String {
    format!(
        "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Ctext y='.9em' font-size='90'%3E{emoji}%3C/text%3E%3C/svg%3E"
    )
}

impl Market {
    fn base_metadata(&self, token_id: &str) -> Option<MtBaseTokenMetadata> {
        self.supply.get(token_id)?;
        let (name, symbol, emoji, _) = describe(token_id)?;
        Some(MtBaseTokenMetadata {
            name,
            id: token_id.to_string(),
            symbol: Some(symbol),
            icon: Some(emoji_icon(emoji)),
            decimals: Some("0".into()),
            base_uri: None,
            reference: None,
            copies: None,
            reference_hash: None,
        })
    }

    fn token_metadata(&self, token_id: &str) -> Option<MtTokenMetadata> {
        self.supply.get(token_id)?;
        let (name, _, _, about) = describe(token_id)?;
        Some(MtTokenMetadata {
            title: Some(name),
            description: Some(about),
            media: None,
            media_hash: None,
            issued_at: None,
            expires_at: None,
            starts_at: None,
            updated_at: None,
            extra: None,
            reference: None,
            reference_hash: None,
        })
    }
}
