// local runs LoveLoop on this machine: workflows run in process on a local
// World in .workflows, updates come from long polling instead of a webhook,
// and check-in chains are restarted at startup. It needs REDIS_URL,
// TELEGRAM_BOT_TOKEN, and AI_GATEWAY_API_KEY or ANTHROPIC_API_KEY.
//
// With -dev, pages load their assets from the Vite dev server (`pnpm dev`
// in web); cmd/dev starts both. Without it, they use the embedded build of
// `pnpm build`.
//
// Long polling turns the bot's webhook off. Connect it again from the
// deployment's setup page afterwards.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eriicafes/go-workflow"
	localworld "github.com/eriicafes/go-workflow/worlds/local"
	"github.com/eriicafes/got"
	"github.com/eriicafes/httpx"
	"github.com/eriicafes/tmpl/vite"

	"github.com/eriicafes/loveloop/web"
	"github.com/eriicafes/loveloop/workflows/flows"
)

func main() {
	port := cmp.Or(os.Getenv("PORT"), "8100")
	dev := flag.Bool("dev", false, "load assets from the Vite dev server")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := got.New()
	runners, err := flows.GetRunners.From(c)
	if err != nil {
		log.Fatal(err)
	}

	world, err := localworld.Open(localworld.Config{
		Dir:     ".workflows",
		BaseURL: "http://localhost:" + port + "/v1/workflow",
	}, runners)
	if err != nil {
		log.Fatal(err)
	}
	defer world.Close()
	if err := prepare(ctx, world); err != nil {
		log.Fatal(err)
	}

	if *dev {
		v, err := vite.New(vite.Config{Dev: true})
		if err != nil {
			log.Fatal(err)
		}
		got.Mock2(c, web.GetVite, v, nil)
	}

	app, err := web.GetApp.From(c)
	if err != nil {
		log.Fatal(err)
	}
	// Runs are recorded in the local World instead of the Vercel World, and
	// the Workflow SDK web UI can inspect them through /v1/workflow.
	app.Client = workflow.NewClient(world)
	app.Workflow = localworld.HTTP(world)

	// Restart the check-in chains a new or wiped .workflows lost. app.Store is
	// not nil here: building the runners above already required the store.
	users, err := app.Store.Users(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, chatID := range users {
		if err := app.EnsureCheckIns(ctx, chatID); err != nil {
			log.Printf("check-ins for %d: %v", chatID, err)
		}
	}
	if err := app.Bot.DeleteWebhook(ctx); err != nil {
		log.Fatal(err)
	}
	go poll(ctx, app)

	log.Printf("LoveLoop is polling for updates; setup page at http://localhost:%s", port)
	if err := httpx.ListenAndServe("localhost:"+port, app.Handler(), nil); err != nil {
		log.Print(err)
	}
}

// prepare prepares world, waiting while a previous process that cmd/dev is
// replacing still holds .workflows.
func prepare(ctx context.Context, world *localworld.World) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := world.Prepare(ctx)
		if !errors.Is(err, localworld.ErrInUse) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func poll(ctx context.Context, app *web.App) {
	var offset int64
	for ctx.Err() == nil {
		updates, err := app.Bot.GetUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("get updates: %v", err)
				time.Sleep(3 * time.Second)
			}
			continue
		}
		for _, update := range updates {
			offset = update.UpdateID + 1
			if err := app.HandleUpdate(ctx, update); err != nil {
				log.Printf("update %d: %v", update.UpdateID, err)
			}
		}
	}
}
