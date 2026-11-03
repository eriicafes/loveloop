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
		BaseURL:  os.Getenv("WORKFLOW_BASE_URL"),
		RedisURL: os.Getenv("REDIS_URL"),
		QueueURL: cmp.Or(os.Getenv("VERCEL_QUEUE_URL"), fmt.Sprintf("https://%s.vercel-queue.com/api/v3", cmp.Or(os.Getenv("VERCEL_REGION"), "iad1"))),
		Topic:    cmp.Or(os.Getenv("VERCEL_WORKFLOW_QUEUE_TOPIC"), DefaultTopic),
	}
	if config.BaseURL == "" && os.Getenv("VERCEL_PROJECT_PRODUCTION_URL") != "" {
		config.BaseURL = "https://" + os.Getenv("VERCEL_PROJECT_PRODUCTION_URL") + "/v1/workflow"
	}
	if token := os.Getenv("QSTASH_TOKEN"); token != "" {
		config.QStash = &vercelworld.QStashConfig{
			BaseURL:           os.Getenv("QSTASH_URL"),
			Token:             token,
			CurrentSigningKey: os.Getenv("QSTASH_CURRENT_SIGNING_KEY"),
			NextSigningKey:    os.Getenv("QSTASH_NEXT_SIGNING_KEY"),
		}
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
