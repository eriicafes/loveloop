package handler

import (
	"log"
	"net/http"
	"os"

	"github.com/eriicafes/go-workflow"
	vercelworld "github.com/eriicafes/go-workflow/worlds/vercel"
	"github.com/eriicafes/got"

	"github.com/eriicafes/loveloop/web"
	"github.com/eriicafes/loveloop/workflows"
)

// indexContainer holds this function's dependencies. Vercel builds every
// file in api as its own function, so each declares its own container.
var indexContainer = got.New()

// index is the App on the Vercel World, with the QStash callback when
// QStash is configured.
var index = got.TryUsing(func(c *got.Container) (http.Handler, error) {
	world, err := workflows.GetWorld.From(c)
	if err != nil {
		return nil, err
	}
	app, err := web.GetApp.From(c)
	if err != nil {
		return nil, err
	}
	app.Client = workflow.NewClient(world)
	if os.Getenv("QSTASH_TOKEN") != "" {
		app.QStash = vercelworld.QStash(world)
	}
	return app.Handler(), nil
})

// Index serves every public route: the setup page and its assets,
// Telegram's webhook, and the QStash callback. vercel.json rewrites all
// paths to it.
func Index(w http.ResponseWriter, r *http.Request) {
	workflows.UseOIDCToken(r)
	handler, err := index.From(indexContainer)
	if err != nil {
		// Build again on the next request.
		indexContainer.ClearErrors()
		log.Printf("index: %v", err)
		http.Error(w, "server is not configured", http.StatusInternalServerError)
		return
	}
	handler.ServeHTTP(w, r)
}
