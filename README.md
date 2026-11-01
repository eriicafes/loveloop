# LoveLoop 💞

A Telegram relationship sidekick. It remembers your loved ones (names,
nicknames, birthdays, likes, love languages, little details), reminds you to
keep in touch, drafts cute texts you can send with one tap, and texts you
first a few times a day at random.

It is a Go app on Vercel built on
[go-workflow](https://github.com/eriicafes/go-workflow): every conversation
turn, check-in day and reminder is a durable run, and the agent is a
[`workflowai`](https://github.com/eriicafes/go-workflow/tree/main/workflowai)
`WorkflowAgent` on [Go AI SDK](https://goaisdk.com), so model calls and tool
steps are recorded and survive restarts.

## How it works

| Workflow | Started by | What it does |
| --- | --- | --- |
| `Chat` | Each Telegram message or button tap | Loads the user, their people, reminders and recent history; runs the agent with its tools; saves the new messages; replies. |
| `CheckIns` | A user's first message, then itself every day | Picks a few random times in the user's waking hours, sleeps until each, and has the agent text first: overdue contacts, upcoming birthdays, a cute text idea, a date idea. Then it sleeps until the day ends and starts the next day's run. |
| `Reminder` | The agent's `scheduleReminder` tool, as a child run | Sleeps until the reminder is due (in steps of at most 6 days), has the agent deliver it, and repeats every `everyDays` days until cancelled. |

The agent's tools: `rememberPerson`, `forgetPerson` (asks for confirmation
with Yes/No buttons through `workflowai` tool approvals), `logContact`,
`draftText` (sends a draft with WhatsApp/Telegram buttons), `scheduleReminder`,
`cancelReminder` (cancels the reminder's run), and `updateSettings`.

There is no cron job. Each user has one chain of `CheckIns` runs, and every
run starts the next before it finishes, so each run's event log stays one day
long. Every message checks that the user's chain is still running and starts
a new one if it failed.

Dependencies are [got](https://github.com/eriicafes/got) constructors kept
beside what they build: `store.GetStore`, `telegram.GetClient`,
`flows.GetModel`, `workflows.GetWorld`, and `web.GetApp`. One container per
process caches them. Workflow runners and steps are methods on `flows.Flows`,
whose fields are their dependencies; `flows.GetRunners` builds it and
registers its runners. Handlers build their workflow client from the World
with `workflow.NewClient`.

Data lives in Redis under `loveloop:*`: users, people, reminders, and the last
messages of each conversation. The workflow World uses the same Redis under
`go-workflow:*`.

```
api/          Vercel functions: index (every public route) and queue (workflow consumer)
web/          App: httpx routes, one file per path (index.go, login.go, telegram.go, ...)
web/templates tmpl pages (struct beside its html) and autoloaded ui components
web/src       Tailwind entry; Vite (package.json and vite.config.ts at the root) builds it into web/dist, which web embeds
workflows/    Workflow types and the World; flows/ holds Flows: the runners, agent, and tools
store/        Redis storage
telegram/     Bot API client
cmd/local     Run everything locally with long polling and a local World
cmd/dev       Run cmd/local -dev and the Vite dev server with live reload (go-dev)
```

## Run locally

Needs Go 1.26, Node with pnpm, Redis, a bot token from
[@BotFather](https://t.me/BotFather), and `AI_GATEWAY_API_KEY` or
`ANTHROPIC_API_KEY`.

```sh
docker run -d -p 6379:6379 redis
pnpm install
export REDIS_URL=redis://localhost:6379 TELEGRAM_BOT_TOKEN=... ANTHROPIC_API_KEY=... SETUP_PASSWORD=...
go run ./cmd/dev
```

`cmd/dev` serves the setup page at http://localhost:8100, rebuilds the server
when Go files change, and reloads the browser when templates change. To run
without Vite, build the assets once with `pnpm build` and run
`go run ./cmd/local`, which serves on http://localhost:8100 too.

Workflows run in process on a local World in `.workflows`. Long polling
removes the bot's webhook, so connect it again from the deployment's setup
page afterwards.

## Deploy to Vercel

1. Create the project and add an Upstash Redis integration (set `REDIS_URL`
   to its `rediss://` URL), and enable Vercel Queues.
2. Set the environment variables from `.env.example`: `TELEGRAM_BOT_TOKEN`,
   `TELEGRAM_WEBHOOK_SECRET`, `SETUP_PASSWORD`, `TOOL_APPROVAL_SECRET`, and
   optionally `TELEGRAM_ALLOWED_USERS` and `MODEL`. The AI Gateway uses the
   deployment's OIDC token, so no model key is needed.
3. Deploy, open the deployment's URL, sign in with `SETUP_PASSWORD`, and
   press **Connect bot**. The page shows whether
   Telegram is delivering to the deployment, the last delivery error, and
   which settings are missing.

`vercel.json` installs and builds the Tailwind assets before the Go
functions compile, so `web/dist` is embedded in them, and serves `web/dist`
as the static output, so only built assets are public files. It subscribes
`api/queue.go` to the `loveloop-workflow` topic, so it has no public URL,
and rewrites every other path to `api/index.go`, which
serves the setup page, Telegram's webhook at `/telegram`, and the QStash
callback. The World's RPC endpoint is not deployed because it has no
authentication; only the signed QStash callback is public.

## Development

`cmd/dev` runs the workflow analyzer (`workflowdev.Check`) before every start
and reload, and CI runs it on every push. It is built from the go-workflow
module in `go.mod`, so it always matches the library.

The setup page is an `App` in `web` whose dependencies are struct fields,
built by `web.GetApp`. Each route is a method in the file named after its
path, and `App.Handler` collects them. Pages are structs in
`web/templates/pages` beside their html, and `web/templates/components/ui`
holds shadcn-style components (`ui/button`, `ui/card`, `ui/input`, ...) that
every page can use with `{{ template "ui/button" props ... }}`. Signing in
sets a signed session cookie for seven days; changing `SETUP_PASSWORD` signs
everyone out.

```sh
go test ./...
dir=$(go list -m -f '{{.Dir}}' github.com/eriicafes/go-workflow)
(cd "$dir" && go build -o /tmp/workflowcheck ./cmd/workflowcheck)
go vet -vettool=/tmp/workflowcheck ./...
```
