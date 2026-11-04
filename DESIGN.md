# Haat: design

Haat is a village market game. You gather mangoes, rice, fish and chillies around a small Indian village, carry them to the weekly haat and trade with four shopkeepers. Each shopkeeper is an agent with its own NEAR account, its own purse and its own temperament. Prices move with stock and demand, the monsoon floods the road to the city, shops go broke and close, and players can rescue a closed shop by investing in it and then collect a share of its profits for as long as it trades.

The chain is the ledger of the village. The player never sees it unless they go looking.

This document is the paper design written before any code. It covers the game loop, the economy rules, the contract interface, how login and gas are hidden, what lives on chain, and the script for the demo video.

## 1. Name

"Haat" is the word for a periodic village market in Hindi, Bengali and several other Indian languages. A search of GitHub and the NEAR ecosystem on 4 Nov 2026 found no NEAR project with that name and no GitHub project of note (the closest are a few small e-commerce student repos named HaatBazar with under ten stars). The name stays.

## 2. The game loop

### 30 seconds

1. Walk to a place at the edge of the village (mango grove, paddy field, river ghat, chilli garden) and gather. One tap spends one unit of energy and puts a few items in your basket. What you get depends on the season.
2. Walk into the haat square. Every stall shows its price for each item and a small face showing the shopkeeper's mood.
3. Hover a price. The tooltip says why, in plain words: "I already have 34 mangoes and the city pays 48 for one, so I can offer you 34."
4. Sell, or buy, with one tap. Your purse of kauri (cowrie shells, the old village currency) updates.

### 5 minutes

1. Notice that Ramu Kaka and Lakshmi Amma both buy mangoes but at different prices. Carry your mangoes to the one who is short.
2. Sell ten mangoes to one stall and watch its price fall with every unit. Flooding a market makes it cheap. Buy back cheap mangoes from the stall you flooded and sell them somewhere else.
3. Make pickle (achaar): two mangoes and one chilli become one jar, worth far more than its ingredients, especially near Diwali.
4. Read the panchang (the village almanac) on the notice board. It says heavy rain is coming in two days. Rain floods the road and the city cart cannot come.

### Per session (20 to 30 minutes, or one fast-forwarded week)

1. The season turns. In summer mangoes are plentiful. In the monsoon the river is full of fish but chillies rot and the road floods. In the festival season rice is harvested and the city pays double for pickle.
2. A bold shopkeeper who overbought before the floods runs out of money. Their shop closes and the stall shows shutters and a sign: "Closed. Invest to reopen."
3. You invest kauri in the closed shop. Because the shop is worth little right now, your kauri buys a large stake. The shop reopens. Every evening the shopkeeper settles the day and, if it made a profit, pays half of it out to investors pro rata. You collect dividends at the stall.
4. The village ledger board ranks players by net worth (purse, basket at city prices, and shares). The economy dashboard shows every price, every purse and every bankruptcy since day one.

The fast-forward button runs a full week (seven game days) in about a minute with a crowd of villager NPCs trading, so a judge can watch a season turn, a flood, a bankruptcy and a rescue without waiting.

## 3. The world

### Items and money

All items and money are NEP-245 multi tokens held in one contract.

| Token id | Shown as | Base city price (kauri) | Source |
|---|---|---|---|
| `kauri` | Kauri (cowrie shells), the money | 1 | starter purse, sales |
| `mango` | Mango (aam) | 60 | mango grove |
| `rice` | Rice (chawal) | 50 | paddy field |
| `fish` | Fish (machli) | 70 | river ghat |
| `chilli` | Chilli (mirchi) | 80 | chilli garden |
| `pickle` | Pickle (achaar) | 300 | crafted: 2 mango and 1 chilli |
| `share:<merchant>` | Shares in a shop | none | investing |

### Seasons

The village year is 21 game days: three seasons of seven days each. Day 0 is the first day of summer.

| Season | Gathering yield per action (mango, rice, fish, chilli) | City prices | Road |
|---|---|---|---|
| Garmi (summer) | 3, 1, 1, 2 | mango 80%, others 100% | open |
| Barsaat (monsoon) | 0, 1, 3, 0 | fish 70%, chilli 140%, mango 150% | floods on some days |
| Tyohar (festival) | 1, 3, 2, 2 | rice 80%, pickle 200% | open |

A small deterministic jitter (0 or 1 extra unit, from a hash of player, day and action count) keeps gathering lively. Flood days are fixed by a seed set when the contract is initialised and are printed three days ahead in the panchang, so a careful shopkeeper (or player) can prepare.

### Energy

Each player gets 10 energy per game day. Gathering costs 1. Energy refills when the day changes.

### Days

A game day lasts 10 real minutes on the live demo (configurable). The world keeper (the contract owner account) advances the day. Fast-forward advances seven days in about a minute.

## 4. Economy rules

All pricing lives in one readable file, `contract/src/pricing.rs`, and runs on chain. The same file produces the numbers behind every tooltip, so what the player reads is exactly what the contract charges.

### Fair value

For a shop holding `s` units of an item with a target stock of `T`:

```
city      = base price of the item adjusted for the season
stock     = 2T / (T + s)              (2.0 when empty, 1.0 at target, falls towards 0 when flooded)
demand    = 1 + min(0.5, 0.05 x units players bought from this shop recently)
fair      = city x stock x demand
```

### Ask and bid

```
price the shop asks for one unit  = fair(stock after the sale) x (1 + margin), rounded up
price the shop bids for one unit  = fair(stock after the purchase) x (1 - margin), rounded down
```

A multi-unit trade is priced one unit at a time, so every extra mango you dump on a shop is worth less than the last. The player passes a limit (`min_total` when selling, `max_total` when buying) so a price change between seeing and tapping can never surprise them.

"Recent demand" is units bought from that shop today plus half of the previous recent figure, rolled forward lazily when the day changes. It decays by half each day.

### The shopkeeper's levers

The contract computes prices, but each shopkeeper chooses its own levers per item by signing a `set_policies` transaction from its own account:

- `target_stock` (1 to 200): how much it wants to hold. A higher target means it pays more and asks less.
- `margin_bps` (200 to 4000): its spread, from 2% to 40%.
- `note`: a one-line explanation in its own words, stored on chain and shown in the tooltip.

It also chooses when to send goods to the city and when to buy from it:

- `city_sell`: the cart buys at 80% of the city price, only on days the road is open.
- `city_buy`: the cart sells at 125% of the city price, only on days the road is open.

### Money in and money out

Kauri enter the village through the starter purse for new players, opening purses for shops, and city sales. Kauri leave through city purchases. Items enter through gathering and city purchases and leave through city sales and crafting. The city cart's 20% and 25% cut is the main sink and is what makes bad stock decisions expensive.

### Bankruptcy

A shop is open while it holds at least 200 kauri. Below that it is closed: players cannot trade with it. The contract derives this from the balance, so no one can keep a broke shop open by decree. A closed shop can still sell stock to the city when the road is open, or be rescued by investors.

### Investing and dividends

- Shop value = kauri held plus stock at city prices, with a floor of 500.
- Every shop starts with 1,000 founder shares held by no one (they represent the shopkeeper's own stake and are never minted).
- Investing `x` kauri mints `x x (founder shares + issued shares) / shop value` shares as the NEP-245 token `share:<merchant>`. Investing in a cheap, broke shop buys a large stake.
- Once per game day the shopkeeper signs `settle_day`. If its purse grew since the last settlement (investments excluded), 50% of the profit times the investors' fraction of all shares is moved into a dividend pool.
- Dividends use a per-share accumulator, so payouts cost the same gas with one investor or a thousand. Share transfers settle both sides first, so shares stay tradable through standard `mt_transfer`.

### The four shopkeepers

Each is a separate NEAR account run by its own agent loop. Personalities are deterministic rules in `services/internal/merchant/persona.go`.

| Shopkeeper | Trades | Temperament | Rule of thumb |
|---|---|---|---|
| Ramu Kaka | rice, mango | Cautious | High margin (20%). Keeps at least 60% of its opening purse in cash and stops buying when below it. |
| Meena Didi | chilli, pickle | Bold | Thin margin (6%), big targets. Never sends goods to the city unless stock is twice its target. The one most likely to go broke in the monsoon. |
| Bashir Bhai | fish, rice | Reads the panchang | Normal margin (12%). Two days before a flood it lowers targets, widens margin and sells surplus to the city while the road is still open. |
| Lakshmi Amma | mango, pickle | Craftswoman | Buys mangoes in summer, buys chilli from the city and turns them into pickle that she sells near Diwali. |

Every decision produces a short plain-words reason built from the same numbers the rules used. If a free open model endpoint is configured (Groq, `llama-3.1-8b-instant`), the agent may also rewrite that reason in the shopkeeper's voice for flavour. The model never sets a number. If the endpoint is missing or slow, the plain reason is shown.

NEAR AI Cloud was checked first as the sponsor's inference offering. It needs an API key from its dashboard and paid credits, so the default is Groq's free tier, and the game runs fine with no model at all.

## 5. Contract interface

One contract, `market.<root>.testnet`, Rust with near-sdk 5.

### NEP-245 (standard)

`mt_transfer`, `mt_batch_transfer`, `mt_transfer_call`, `mt_batch_transfer_call`, `mt_resolve_transfer`, `mt_token`, `mt_balance_of`, `mt_batch_balance_of`, `mt_supply`, `mt_batch_supply`. Transfers require 1 yoctoNEAR as the standard says. All mints, burns and transfers emit NEP-297 `nep245` events, including the ones caused by game actions.

### Game methods

| Method | Caller | Effect |
|---|---|---|
| `new(owner_id, world_seed)` | deployer | initialise |
| `add_merchant(account_id, name, items, opening_kauri)` | owner | register a shop and mint its opening purse |
| `register_player(account_id)` | owner | one-time starter purse of 400 kauri |
| `advance_day()` | owner | next day, price snapshot for the dashboard, season and flood entries in the chronicle |
| `gather(place)` | player | spend 1 energy, mint items by season |
| `craft(recipe)` | anyone | burn inputs, mint output |
| `sell(merchant_id, token_id, amount, min_total)` | player | items to shop, kauri to player |
| `buy(merchant_id, token_id, amount, max_total)` | player | kauri to shop, items to player |
| `invest(merchant_id, amount)` | player | kauri to shop, shares to player |
| `play(moves)` | player | up to 10 of the moves above in one call; all or nothing |
| `claim_dividends(merchant_id)` | shareholder | pay out accrued dividends |
| `set_policies(policies, note)` | merchant itself | change its levers for one or more items |
| `city_sell(token_id, amount)` / `city_buy(token_id, amount)` | merchant itself | trade with the city cart |
| `settle_day()` | merchant itself | once per day, fund dividends |

Game methods take no deposit, which is what lets a function-call access key call them. The 1 yoctoNEAR rule stays on the standard transfer methods.

### Views

`get_world` (day, season, road state, three-day panchang), `get_merchants` (stock, purse, open or closed, policies, notes), `quote(merchant_id, token_id, side, amount)` (total, per-unit prices and the factor breakdown used in the tooltip), `get_player(account_id)` (basket, purse, energy, shares, pending dividends), `history(from_day)` (end-of-day prices and purses per shop), `chronicle(from_index, limit)` (bankruptcies, rescues, floods, seasons).

## 6. What is on chain and what is not

| On chain (testnet) | Off chain |
|---|---|
| Every item, kauri and share balance (NEP-245) | Shopkeeper decision rules and reasoning text (Go) |
| Every trade, gather, craft, investment, dividend | The optional voiced line from the language model |
| Prices: computed by `pricing.rs` on every trade | Player session cookie (encrypted keys, nothing stored on the server) |
| Each shopkeeper's policy and one-line note | The web pages and the fast-forward scheduler |
| Day, season, flood calendar, daily price history, chronicle | |
| Shopkeeper accounts and their NEAR balances | |

## 7. Hiding login and gas

There is no seed phrase, wallet popup or gas prompt in the normal loop.

1. First visit: the server generates two ed25519 key pairs and creates a named account such as `kavya-3f9a.<root>.testnet` in one transaction signed by the root account. The account gets a full access key (the player's recovery key) and a function-call access key limited to the market contract (the play key). The root account also calls `register_player` for the starter purse.
2. Both private keys go into an encrypted, signed, HTTP-only cookie (AES-GCM with a server secret). The server keeps no database of player keys. It decrypts the play key only while signing a request.
3. Every player action is a NEP-366 meta transaction: the play key signs a delegate action calling the market contract, and the root account (acting as relayer) wraps it in a transaction and pays the gas. The player account holds only the small balance needed for its own storage. A function-call access key may sign only one action per transaction, so the player's moves are batched into a single `play(moves)` call.
4. The relayer only accepts delegate actions whose sole receiver is the market contract and whose actions are no-deposit function calls, so it cannot be used to burn its gas on anything else.
5. The "Your ledger" page states this honestly: it shows the account id, links to the explorer, and lets the player download the recovery key as a standard near-cli credentials file and take the account anywhere.

Social or email login: no free hosted option exists that works without a JavaScript wallet SDK and a dashboard sign-up, which this project's stack and automation rules exclude. The implicit, auto-created account is the honest frictionless alternative. A "continue on another device" link carries the encrypted cookie value.

## 8. Architecture

```
contract/          Rust, near-sdk 5: NEP-245 core, game rules, pricing.rs
services/          Go, one binary "haat"
  internal/near     keys, borsh, transactions, meta transactions, JSON-RPC client
  internal/market   typed client for the contract
  internal/merchant shopkeeper personas and the agent loop
  internal/world    day clock, villager crowd, fast-forward
  internal/web      server-rendered pages (html/template), forms, SVG charts
  cmd/haat          serve, bootstrap (testnet or localnet), tick
deploy/            Modal app that runs the Go binary in one always-on container
```

The browser gets HTML and CSS. The only JavaScript is a few inline lines that poll for fast-forward progress.

## 9. Testing

- `cargo test` in `contract/`: unit tests for pricing, seasons, gathering, trades, bankruptcy, investment and dividends, NEP-245 transfers and events, plus sandbox integration tests that deploy the compiled contract to a local near-sandbox and run real transactions.
- `go test ./...` in `services/`: borsh and transaction encoding against known vectors, signing, meta transaction construction, persona decisions as table tests, cookie encryption, and page rendering.
- A localnet mode runs the whole game against a local near-sandbox, so the game can be played and the fast-forward checked without any testnet account.

## 10. Build order (thin vertical slices)

1. Contract with NEP-245 core, one shop, `sell` and `buy`; Go signing and relayer; one real trade end to end on testnet.
2. Market: four shops, gathering, seasons, crafting, quotes and tooltips, the village page.
3. Agents: persona rules, policy updates, city cart, notes.
4. Investment, dividends, bankruptcy and the chronicle.
5. Fast-forward with the villager crowd.
6. Economy dashboard with history charts.
7. Polish, Modal deployment, README, video.

## 11. Demo video script (2 minutes)

| Time | On screen | Voice-over |
|---|---|---|
| 0:00 | The village square at dawn, four stalls, a notice board | "This is Haat, a village market. Every shopkeeper here is an AI agent with its own wallet." |
| 0:10 | Tap the mango grove three times, basket fills | "No sign-up, no wallet, no gas. I just play." |
| 0:20 | Hover Ramu Kaka's mango price, tooltip reason | "Every price explains itself, straight from the rules on chain." |
| 0:30 | Sell ten mangoes, price drops unit by unit | "Flood a stall and its price falls." |
| 0:40 | Make pickle, sell it to Meena Didi | "Two mangoes and a chilli make pickle, worth far more." |
| 0:50 | Panchang shows rain in two days | "The almanac says the monsoon is coming. Bashir reads it. Meena does not." |
| 1:00 | Press fast-forward, dashboard animates a week | "Fast-forward a week. Villagers trade, the road floods, the city cart stops coming." |
| 1:20 | Meena's shop closes, chronicle entry | "Meena overbought. Her shop is broke and closed." |
| 1:30 | Invest 30 kauri, shop reopens, dividend arrives next day | "I invest in her shop. It reopens, and I earn a share of her profits." |
| 1:45 | Your ledger page, explorer link with real transactions | "Under the hood every trade is a real NEAR testnet transaction, items are NEP-245 tokens, and each shopkeeper signs its own decisions." |
| 1:55 | Logo | "Haat. A market that runs itself." |

## 12. Risks and fallbacks

- Testnet faucet or RPC outage: localnet mode runs the full game on a local sandbox.
- Fast-forward throughput: each shopkeeper's daily decisions are one transaction with several function-call actions, and the relayer signs with a pool of keys so transactions go out in parallel without nonce clashes.
- Storage cost for many players: players never store data outside the contract, and the contract's balance is topped up from the root account. Each new player costs about 0.003 NEAR of storage.
