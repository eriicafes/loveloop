package handler

import (
	"log"
	"net/http"

	vercelworld "github.com/eriicafes/go-workflow/worlds/vercel"
	"github.com/eriicafes/got"

	"github.com/eriicafes/loveloop/workflows"
	"github.com/eriicafes/loveloop/workflows/flows"
)

// queueContainer holds this function's dependencies. Vercel builds every
// file in api as its own function, so each declares its own container.
var queueContainer = got.New()

// queue consumes deliveries of the Vercel World with LoveLoop's runners.
var queue = got.TryUsing(func(c *got.Container) (http.Handler, error) {
	world, err := workflows.GetWorld.From(c)
	if err != nil {
		return nil, err
	}
	runners, err := flows.GetRunners.From(c)
	if err != nil {
		return nil, err
	}
	return vercelworld.Queue(world, runners), nil
})

// Queue consumes the workflow topic and runs workflows and their steps.
// vercel.json gives it a queue trigger, so it has no public URL.
func Queue(w http.ResponseWriter, r *http.Request) {
	workflows.UseOIDCToken(r)
	handler, err := queue.From(queueContainer)
	if err != nil {
		// Build again on the next delivery.
		queueContainer.ClearErrors()
		log.Printf("queue: %v", err)
		http.Error(w, "server is not configured", http.StatusInternalServerError)
		return
	}
	handler.ServeHTTP(w, r)
}
