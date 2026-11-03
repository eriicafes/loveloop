package workflows

import (
	"cmp"
	"fmt"
	"net/http"
	"os"

	vercelworld "github.com/eriicafes/go-workflow/worlds/vercel"
	"github.com/eriicafes/got"
)

// DefaultTopic is the Vercel Queues topic vercel.json subscribes the queue
// function to.
const DefaultTopic = "loveloop-workflow"

// GetWorld returns the Vercel World configured from the environment. It
// shares REDIS_URL with the store, over TCP: Vercel keeps Go function
// instances warm, so the connection pool outlives a request. Clients are
// built from it with workflow.NewClient.
var GetWorld = got.TryUsing(func(c *got.Container) (*vercelworld.World, error) {
	config := vercelworld.Config{
		RedisURL: os.Getenv("REDIS_URL"),
		QueueURL: fmt.Sprintf("https://%s.vercel-queue.com/api/v3", cmp.Or(os.Getenv("VERCEL_REGION"), "iad1")),
		Topic:    DefaultTopic,
	}
	if os.Getenv("VERCEL_PROJECT_PRODUCTION_URL") != "" {
		config.BaseURL = "https://" + os.Getenv("VERCEL_PROJECT_PRODUCTION_URL") + "/v1/workflow"
	}
	return vercelworld.New(config)
})

// UseOIDCToken makes the OIDC token of a Vercel function's request visible
// to the World and the AI Gateway, which read it from VERCEL_OIDC_TOKEN.
// Vercel sends it with every request and rotates it.
func UseOIDCToken(r *http.Request) {
	if token := r.Header.Get("x-vercel-oidc-token"); token != "" && token != os.Getenv("VERCEL_OIDC_TOKEN") {
		os.Setenv("VERCEL_OIDC_TOKEN", token)
	}
}
