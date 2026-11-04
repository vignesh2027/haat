# Haat: design

Haat is a village market game. You gather mangoes, rice, fish and chillies around a small Indian village, carry them to the haat and trade with four shopkeepers. Each shopkeeper is an agent with its own NEAR account, its own purse and its own temperament. Prices move with stock and demand, the monsoon floods the road to the city, shops go broke and close, and players can rescue a closed shop by investing in it and then collect a share of its profits.

The chain is the ledger of the village. The player never sees it unless they go looking.

This document started as the paper design written before any code, and has been kept in line with what is built. Items that are not built are marked as such.

## 1. Name

"Haat" is the word for a periodic village market in Hindi, Bengali and several other Indian languages. The name was checked on 4 Nov 2026:

- **NEAR ecosystem:** no NEAR project, dapp or token named Haat in a web search of the NEAR ecosystem and DappRadar listings. The nearest names are unrelated tokens such as HAT.
- **GitHub:** `gh search repos haat` finds no project of note. The closest are small student e-commerce repositories named HaatBazar (under ten stars) and unrelated tools (haatkit, haathumb).
- **Tokens:** no token with the symbol or name HAAT on CoinMarketCap or CoinGecko. Similar names are Joe Hat Token (HAT), Hatcoin (HTC) and Human Asset Token (HATOK).
- **Apps and games:** no app or game named exactly Haat in web and app store results.
- **Trademarks and brands:** "Haat" alone is a generic word for a market. Indian filings found use it only inside compound names in other classes, such as "Pahari Haat" (food, class 30) and "Uphaar Haat" (business services, class 35). Marketplaces such as Jaipuri Haat and Tribes Haat, and government craft markets such as Dilli Haat, also use it in compound names. A Japanese fashion line called HaaT (clothing, class 25) existed under Issey Miyake. None of these is a game, a software product or a blockchain project. Haat is a game (classes 9 and 41) and always appears with its own logo.

Sources: en.wikipedia.org/wiki/Haat_bazaar, registerkaro.in trademark search listings for "Pahari Haat" and "Uphaar Haat", coinmarketcap.com search for HAT tokens, and GitHub search. Before any commercial use, a formal trademark search in IP India and WIPO should be done; for a hackathon entry the name is clear to use.

The logo is original: a scalloped market awning over a cowrie shell (kauri, the old village money), in the game's blue and violet. It shares no shapes or colours with NEAR's marks. Files: `services/internal/web/static/logo.svg` and `logo-512.png`.

## 2. The game loop

### 30 seconds

1. Tap a place at the edge of the village (mango grove, paddy field, river ghat, chilli garden). Each tap spends one energy and adds a few items to your basket. What you get depends on the season. Taps return at once and are written to the chain in a batch about a second later.
2. Look at the haat square. Every stall shows its price for each item and the shopkeeper's mood.
3. Hover a price. The tooltip says why, in plain words: "I already have 34 mangoes and the city pays 48 for one, so I can offer you 31."
4. Sell or buy with one tap. Your purse of kauri (cowrie shells) updates.

### 5 minutes

1. Ramu Kaka and Lakshmi Amma both buy mangoes, at different prices. Carry your mangoes to the one who is short.
2. Sell ten mangoes to one stall and watch the price fall with every unit. Flooding a market makes it cheap.
3. Make pickle (achaar): two mangoes and one chilli become one jar, worth far more, especially near Diwali.
4. Read the panchang (the village almanac). It says when the rains will flood the road and the city cart will not come.
5. Open "How Meena Didi decided lately" on a stall to see what the shopkeeper chose and why.

### Per session (or one fast-forwarded week)

1. The season turns. Summer brings mangoes. The monsoon fills the river with fish, rots the chillies and floods the road. The festival season brings the rice harvest, and the city pays double for pickle.
2. A bold shopkeeper overbuys and runs out of money. The stall shows shutters and a sign: "Closed. Invest to reopen."
3. You invest kauri in the closed shop. Because the shop is worth little, your kauri buys a large stake. The shop reopens, and when it closes a day above its best purse so far, half of that profit, times the investors' share, is paid out as dividends.
4. The goal for the year: be the richest trader on Diwali, the last day of the festival season. Each shop you rescue earns a lamp on the leaderboard.
5. The market dashboard shows every price, every purse and every bankruptcy, read from the contract.

Fast-forward plays one day or seven days back to back (about 20 seconds on the local node, about three minutes on testnet, where every day is several rounds of real transactions) with six villager NPCs trading, so a judge can watch a season turn, a flood, a bankruptcy and a rescue.

## 3. The world

### Items and money

All items, money and shares are NEP-245 multi tokens in one contract, with NEP-245 metadata (names, symbols, icons, descriptions).

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

The village year is 21 game days: three seasons of seven days. Day 0 is the first day of summer.

| Season | Gathering yield per trip (mango, rice, fish, chilli) | City prices | Road |
|---|---|---|---|
| Garmi (summer) | 3, 1, 1, 2 | mango 80%, others 100% | open |
| Barsaat (monsoon) | 0, 1, 3, 0 | fish 70%, chilli 140%, mango 150% | floods on some days |
| Tyohar (festival) | 1, 3, 2, 2 | rice 80%, pickle 200% | open |

A lucky trip gives one more unit about half the time. Flood days come from a seed set at initialisation and are shown three days ahead in the panchang.

### Energy and days

Each player gets 10 energy per game day. A game day lasts 15 real minutes on the hosted demo and 2 on the local node (`HAAT_DAY_MINUTES`). The clock stops when nobody has played for 30 minutes.

## 4. Economy rules

### Prices

All pricing lives in one readable file, `contract/src/pricing.rs`, and runs on chain. The tooltip numbers come from the same values.

```
city      = base price of the item adjusted for the season
stock     = 2T / (T + s)        (2 when empty, 1 at target, towards 0 when flooded)
demand    = 1 + min(0.5, 0.05 x units players bought from this shop recently)

price the shop asks for one unit  = city x stock(after the sale) x demand x (1 + margin), rounded up
price the shop bids for one unit  = city x stock(after the purchase) x (1 - margin), rounded down
```

Demand raises only the asking price. It never raises what a shop pays, so buying and selling straight back always loses money; a property test checks this over many shelves. A multi-unit trade is priced one unit at a time. Players pass a limit (`min_total`, `max_total`). Recent demand halves every day.

### The shopkeeper's levers

The contract computes prices; each shopkeeper chooses its own levers per item and signs them from its own account:

- `target_stock` (1 to 200) and `margin_bps` (200 to 4000), enforced by the contract.
- City cart trades: the cart buys at 80% of the city price and sells at 125%, and never comes on flood days.
- Pickle to make, and a note of at most 200 characters, stored on chain.

### Bankruptcy, investment and dividends

- A shop with less than 200 kauri is closed. The contract checks the purse on every trade, and a standard kauri transfer opens or closes a shop at once.
- Shop value is kauri plus stock at city prices, with a floor of 500. Every shop has 1,000 founder shares that are never minted. Investing `x` kauri mints `x x (founder + issued shares) / value` shares.
- Dividends come only from new highs of the shop's purse (a high-water mark): when a shop closes a day above its best purse so far, half of the rise, times the investors' fraction of all shares, goes to the dividend pool. Cash that goes out and comes back is never paid twice.
- Profit made before an investment is paid to the existing owners at the moment of the investment, so a late investor does not share it.
- A per-share accumulator pays any number of holders at the same gas cost. Share transfers settle both sides first, so shares stay tradable through `mt_transfer`.

### The four shopkeepers

| Shopkeeper | Trades | Temperament | Rule book |
|---|---|---|---|
| Ramu Kaka | rice, mango | cautious | 20% margin; keeps 60% of his opening purse in cash and buys little below it |
| Meena Didi | chilli, pickle | bold | 6% margin, big targets; bets her purse on chillies before the rains and on pickle before Diwali; careful after a rescue until her cash box is one and a half times its opening size |
| Bashir Bhai | fish, rice | reads the panchang | 12% margin; two days before a flood he widens his margin, sells extra fish to the city and stocks rice while the road is open, then buys the fish glut cheaply during the floods |
| Lakshmi Amma | mango, pickle | craftswoman | buys mangoes in summer, buys chillies from the city, makes pickle and sells it near Diwali |

### Character limits

Each shop's character is stored in the contract when it opens, and the contract refuses decisions outside it:

| Shopkeeper | Margin | Largest target | Cash reserve | Also |
|---|---|---|---|---|
| Ramu Kaka | 18% to 25% | 20 | 1,800 kauri | |
| Meena Didi | 4% to 18% | 60 | none: she can go broke | |
| Bashir Bhai | 10% to 25% | 25 | 1,250 kauri | buys from the city only when the panchang shows a flood within two days |
| Lakshmi Amma | 10% to 20% | 30 | 600 kauri | |

A shop never spends its reserve, neither on players' goods nor on the city cart, so the prudent shops cannot be driven to closing. Only bold Meena can go broke, which is her story.

### How a shopkeeper decides

Each review, the agent reads its shop and the world from the chain. Its rule book (`services/internal/merchant/persona.go`) proposes a decision: levers, city cart trades, pickle to make and a note. If an open model is configured (Groq's free tier, the open-weights `openai/gpt-oss-20b`; `services/internal/merchant/brain.go`), the model makes the day's key choice, once per shopkeeper per game day: target stock and margin for each item, and the note. Later reviews the same day reuse that choice, and a day whose situation matches one seen before (same season, weather, cash band and stock levels) reuses that day's choice without calling the model, so the free allowance lasts through a fast-forward. City trades and crafting stay with the rule book, which sizes them to the shop's cash, reserve and stock. Every decision is clamped to the character limits before signing. If the model is slow, over its allowance or gives an unusable answer, the rule book decides. The stall shows which way each decision went, the rule book's suggestion and the signed transaction.

NEAR AI Cloud was checked first as the sponsor's inference offering. It needs a dashboard key and paid credits, so the default is Groq's free tier.

### Villagers

Six villager NPCs (mango picker, fisher, pickle maker, chilli farmer, trader, investor) play through exactly the same path as a human: a play key, one batched `play()` call per day, and gas paid by the relayer. The investor rescues a shop that has been closed for a full day, which leaves a human player a day to do it first. Villagers are not ranked on the leaderboard, since they play every day whether anyone else does.

## 5. Contract interface

One contract, `market.<root>`, Rust with near-sdk 5.

**NEP-245 core:** `mt_transfer`, `mt_batch_transfer`, `mt_transfer_call`, `mt_batch_transfer_call`, `mt_resolve_transfer` (private; takes the single sender, which the standard allows), `mt_token`, `mt_balance_of`, `mt_batch_balance_of`, `mt_supply`, `mt_batch_supply`. Transfers need 1 yoctoNEAR. Every mint, burn and transfer, including those from game actions, emits a NEP-297 `nep245` event. Refund gas grows with the number of tokens.

**NEP-245 metadata:** `mt_metadata_contract`, `mt_metadata_token_all`, `mt_metadata_token_by_token_id`, `mt_metadata_base_by_token_id`, `mt_metadata_base_by_metadata_id`.

**Storage:** players, shops and the contract can receive tokens. Any other account pays `storage_deposit` (0.01 NEAR) once first, so transfers cannot make the contract pay for storage. `is_registered` tells which accounts can receive.

**Game methods:**

| Method | Caller | Effect |
|---|---|---|
| `new(owner_id, world_seed)` | deployer | initialise |
| `reset_world(owner_id, world_seed, world_name)` | the market account itself | start a fresh world on a deployed market, under a new storage name |
| `add_merchant(account_id, name, items, opening_kauri, opening_stock, bounds)` | owner | open a shop with its character limits |
| `register_player(account_id)` | owner | one-time starter purse of 400 kauri |
| `advance_day()` | owner | next day, price snapshot, season and flood entries |
| `gather`, `craft`, `sell`, `buy`, `invest`, `claim_dividends` | player | the moves |
| `play(moves)` | player | up to 10 moves in one call, all or nothing |
| `set_policies(policies, note)` | shop itself | change its levers, inside its character limits |
| `set_bounds(account_id, bounds)` | owner | change a shop's character limits |
| `city_sell`, `city_buy` | shop itself | trade with the city cart |
| `settle_day()` | shop itself | once a day, fund dividends from new highs |

Game methods take no deposit, which lets a function-call access key call them.

**Views:** `get_world`, `get_merchants`, `get_merchant`, `quote`, `get_player`, `history` (the last 60 days are kept), `today`, `chronicle` (the last 400 entries are kept), `chronicle_len`, `leaderboard`.

## 6. What is on chain and what is not

| On chain (testnet) | Off chain |
|---|---|
| Every item, kauri and share balance (NEP-245) | Shopkeeper rule books and the model calls |
| Every trade, gather, craft, investment, dividend | The move queue and the village feed (in memory) |
| Prices, computed by `pricing.rs` on every trade | Player cookies (encrypted keys; nothing stored on the server) |
| Each shopkeeper's levers and signed note | The web pages, fast-forward scheduler and limits |
| Day, season, flood calendar, 60 days of price history, chronicle, leaderboard data | |
| Shopkeeper and villager accounts and their NEAR balances | |

## 7. Hiding login and gas

1. "Enter the haat" takes a ready-made account from a small pool (or creates one): a named account such as `kavya-3f9a.<root>.testnet` with a full-access recovery key and a play key limited to the market contract, registered for the starter purse.
2. Both keys are sealed into an encrypted, tamper-proof cookie (AES-GCM with a server secret). The server keeps no database of keys and opens the cookie only while signing.
3. Every move is a NEP-366 meta transaction signed by the play key. The root account relays it and pays the gas. A function-call access key may sign only one action, so moves are batched into one `play(moves)` call.
4. The relayer only relays no-deposit calls to the market contract.
5. "Your ledger" states this plainly and has "Take your account with you": it shows the recovery key once, offers a near-cli credentials file and removes the key from the cookie.

No free social or email login works without a JavaScript wallet SDK and a dashboard sign-up, which this stack excludes, so the game uses this auto-created account instead and says so.

### Protecting the shared demo

- Entering is limited per IP address, and the enter button must be pressed on the page.
- Moves are limited per account and per IP address, and checked against the player's state minus queued moves before anything is relayed.
- Relayed transactions have a daily budget, relaying stops if the root account falls below 0.5 NEAR, and fast-forward is capped per hour and needs a player session.
- A watchdog tops up the market contract's storage from the root account before it runs out.

## 8. Architecture

```
contract/          Rust, near-sdk 5: NEP-245 core and metadata, game rules, pricing.rs
services/          Go, one binary "haat"
  internal/near     keys, borsh, transactions, meta transactions, relayer, JSON-RPC client
  internal/market   typed client for the contract
  internal/merchant personas, rule books, the model brain, the agent loop
  internal/world    day clock, villagers, fast-forward, storage watchdog
  internal/web      server-rendered pages, move queue, limits, SVG charts
  cmd/haat          bootstrap, serve, localnet, fast-forward, smoke, status, reset-market
deploy/            Modal app that runs the binary in one always-on container
```

The browser gets HTML and CSS plus one small inline script that sends moves in the background and polls until they land.

## 9. Testing

- `cargo test --lib`: pricing, seasons, gathering, trades, bankruptcy, investment and dividends, NEP-245 transfers, events and metadata, storage registration, history limits, leaderboard, and regression tests for every economic finding of the independent review.
- `cargo test --test sandbox`: deploys the compiled contract to a local near-sandbox and plays real transactions, including the `mt_transfer_call` refund path.
- `go test ./...`: borsh layouts, signing, meta transactions, relayer policy, rule books, model answer validation and spending guards, villager plans, the move queue, limits, cookies and page rendering.
- `make story` plays a seeded week (floods on days 7, 8 and 10) on a private local node with the rule books and asserts the story: Meena goes broke and is rescued, Ramu, Bashir and Lakshmi never close, and Bashir's purse ends the week above where it started.
- `make localnet` and `make fast-forward` run the whole game on a local node.

## 10. Demo video script (2 minutes)

| Time | On screen | Voice-over |
|---|---|---|
| 0:00 | The welcome page, logo, four stalls | "This is Haat, a village market. Every shopkeeper here is an agent with its own wallet." |
| 0:10 | Enter, tap the mango grove three times, basket fills | "No sign-up, no wallet, no gas. I just play." |
| 0:20 | Hover Ramu Kaka's mango price | "Every price explains itself, from the rules on chain." |
| 0:30 | Sell ten mangoes, price drops unit by unit | "Flood a stall and its price falls." |
| 0:40 | Open "How Meena Didi decided lately" | "Meena thinks for herself with an open model, inside limits the contract enforces, and signs her own decisions." |
| 0:55 | Panchang shows rain | "The almanac says the monsoon is coming. Bashir prepares. Meena bets." |
| 1:05 | Fast-forward, dashboard moves | "A week of trading in seconds. The road floods, the city cart stops." |
| 1:20 | Meena's shutters, chronicle | "Meena bet everything and went broke." |
| 1:30 | Invest, shop reopens, dividend next day, a lamp on the leaderboard | "I rescue her shop and earn from her recovery." |
| 1:45 | Your ledger, explorer with real transactions | "Every item is a NEP-245 token and every move a real NEAR transaction." |
| 1:55 | Logo | "Haat. A market that runs itself." |

## 11. Not built

- NEAR Intents (cashing kauri out) and social login.
- An illustrated village map; the square uses cards and emoji.
