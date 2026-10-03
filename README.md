# telebeans

A personal Telegram bot that records expenses through Fava. Go prototype with explicit category aliases and inline buttons.

## Run

Requires Go 1.25+ and network access to Telegram and Fava. Long polling means there is no public webhook to host.

1. Copy `config.example.json` to `config.json` and replace the example account names with accounts from your ledger. Create a bot with BotFather, copy `.env.example` to `.env`, and set `TELEGRAM_BOT_TOKEN`.
2. Build and get your numeric Telegram user ID:

   ```sh
   go build -o bin/telebeans ./cmd/telebeans
   bin/telebeans whoami
   ```

   Send `/start` in the bot's private chat. Copy the printed `TELEGRAM_USER_ID` into `.env`.
3. Set `FAVA_URL` to your **ledger URL**, including its slug, for example `https://fava.example.test/ledger`.
4. Set Fava's insertion destination in the production ledger, using an absolute path **inside Fava's server/container**:

   ```beancount
   2022-10-15 custom "fava-option" "default-file" "/ledger/current.beancount"
   ```

   The file must already exist and be included in the ledger. Fava's own insertion rules apply. The sample Beancount files in this repository are never used by the bot.
5. Check the connection and start:

   ```sh
   bin/telebeans check
   bin/telebeans
   ```

The bot reads `.env` from the working directory. Existing environment variables take precedence. Messages and buttons are accepted only from `TELEGRAM_USER_ID` in a private chat. Stop with Ctrl+C.

## Docker

Set up your local `config.json` and `.env` as above. Docker builds the Go binary and runs it with CA certificates for HTTPS and timezone data for the configured timezone.

```sh
mkdir -p data
docker compose build
docker compose run --rm telebeans check
docker compose up -d
docker compose logs -f telebeans
```

If you still need your Telegram user ID, use `docker compose run --rm telebeans whoami` and add the printed ID to `.env` before starting the service.

Compose mounts `config.json` read-only and persists SQLite state in `./data/telebeans.db`. `TELEBEANS_CONFIG` in `.env` can select another host configuration file; inside the container it is always mounted at `/app/config.json`. Credentials are passed from `.env` at runtime. The build context excludes local config, credentials, databases, and ledger files.

The container must resolve and reach your LAN Fava URL, plus Telegram's API. It uses outbound long polling and needs no published ports. Fava controls the production ledger files through its API.

Stop the local bot before starting the container so only one instance polls Telegram. To carry over existing bot state, stop the local bot, create `data/`, and copy `telebeans.db` into `data/telebeans.db` before starting Compose.

After editing `config.json`, run `docker compose restart telebeans`. After editing `.env` or updating the code, run `docker compose up -d --build`. Stop the deployment with `docker compose down`; the `data/` directory remains on the host.

## Enter an expense

```text
[YYYY-MM-DD | вчера] CATEGORY AMOUNT [CURRENCY] [@PAYMENT] [COMMENT]

дома 350
дома 350 хлеб и молоко
кафе 500. блины
дома 350 @нал
вчера аренда 400 EUR за октябрь
2026-08-29 дома 350
дома 350 хлеб и молоко 2026-09-30
```

- Category aliases live in `config.json`; `/accounts` lists them. A full `Expenses:…` account also works.
- The example configuration defaults to RSD and `Assets:Bank:RSD`. Set your own defaults in `config.json`. `@нал` selects cash in the transaction's currency. Other currencies ask for a payment account before saving.
- Amounts must be positive with up to two fractional digits: `350.50` or `350,50`. A trailing dot separates an integer amount from the comment.
- A comment overrides the alias's narration. Without one, `дома 350` uses `Еда`.
- A standalone `YYYY-MM-DD` sets the transaction date anywhere in the message and is removed from the narration. Use only one date marker; invalid dates and combining an explicit date with `вчера` are rejected. Without a date, the bot uses the Telegram message's send time in Europe/Belgrade.
- Known categories save immediately. Unknown categories show a paginated account picker. After saving, **Запомнить** can make the word an alias.
- Saved entries have payment, edit amount/narration/date, and undo buttons. Payment changes affect that entry; undo deletes it through Fava.
- `/cancel` exits a pending text edit; `/help` shows examples.

Try `дома 350`, change payment to cash, edit the amount, and undo. Verify the entry and its removal in production Fava.

## Buy EUR with RSD

```text
2026-09-12 евро 500 @@ 59000
евро 500 EUR @@ 59000 RSD @bank обмен валюты
```

`евро` (or `EUR`) buys the first amount in EUR for the **total** RSD amount after `@@`. Without a comment, the narration is `купил евро`. Dates work in any position, as for expenses.

By default, both sides use cash: the `нал` payment shortcut's account prefix, or `Assets:Cash` if that shortcut is absent. The first example produces the following entry, plus the bot's `telebeans_id` metadata:

```beancount
2026-09-12 * "купил евро"
  Assets:Cash:EUR  500 EUR @@ 59000 RSD
  Assets:Cash:RSD
```

Beancount infers the RSD debit. `@bank` (or another configured payment shortcut) changes the RSD source account; EUR still goes into cash. The inline payment picker also offers RSD accounts. Change amount asks for both numbers, such as `500 59000`. Description, date, and undo buttons work as usual.

The bot saves through Fava and retains `@@` in the ledger source. No rate calculation or decimal library is needed. This syntax currently supports buying EUR with RSD.

## Monthly bills

Add templates to `reminders` in `config.json` and restart. Example structure (replace the amounts and schedule):

```json
{
  "id": "utilities",
  "narration": "Коммунальные платежи",
  "day": 15,
  "time": "10:00",
  "currency": "RSD",
  "payment": "Assets:Bank:RSD",
  "postings": [
    {"account": "Expenses:Utilities:Water", "amount": "3000"},
    {"account": "Expenses:Utilities:Electricity", "amount": "2500"}
  ]
}
```

The prompt has **Добавить / Изменить сумму / Напомнить позже / Пропустить**. Add records the payment. For split bills, Change amount asks for amounts separated by spaces in the displayed order; the payment posting balances their sum.

Snooze is 24 hours. Skip applies to the current month. Days 29–31 fall back to the month's last day. Reminders run while the bot is running; after restart it catches up within the current month. `/bills` lists templates; `/bill utilities` opens this month's prompt immediately. An already saved bill opens its existing entry.

Reminder IDs: lowercase ASCII letters, digits, `_` or `-`, up to 24 characters.

## Configuration and state

`config.json` is your gitignored local configuration for aliases, payment shortcuts, timezone, and monthly templates. `config.example.json` is the shareable template with generic accounts. Set `TELEBEANS_CONFIG` to use a different configuration file. `TELEBEANS_DB` chooses the SQLite file (default `telebeans.db`). Local state, configuration, credentials, and ledger samples are gitignored.

Fava/Beancount is the source of truth for accounts and saved transactions. SQLite holds pending drafts, learned alias references, Telegram interactions/update offset, ledger IDs, and reminder status. Saved transaction contents are always read from Fava. Entries have `telebeans_id` metadata so buttons find them after their Fava hash changes.

Writes use Fava's `add_entries` and `source_slice` APIs. Editing patches the source returned by Fava with its checksum, keeping metadata and comments. Amounts use integer hundredths; SQLite is the only direct third-party Go dependency.

This prototype targets the deployed Fava 1.30 API. It handles expenses, same-currency bill splits, and EUR purchases with RSD. Other transfers, income, and LLM guessing are later work. Parsing, Fava, Telegram, and state are separate components; a future suggestion provider can produce drafts without owning ledger writes.

## Tests

```sh
go test ./...
```

For the optional real Fava integration test:

```sh
python3 -m venv /tmp/telebeans-fava-test
/tmp/telebeans-fava-test/bin/pip install 'fava==1.30.1' 'beanquery==0.1.0'
go test ./... -fava-test-binary /tmp/telebeans-fava-test/bin/fava
```

The test launches Fava with a temporary ledger and simulates Telegram over HTTP. It exercises add, payment changes, edits, undo, EUR selection, learned aliases, repeated updates, split bills, and EUR purchases with total RSD prices. It never contacts production Fava or Telegram. Python is used only for the test server.
