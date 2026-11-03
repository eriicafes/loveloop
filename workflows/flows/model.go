package flows

import (
	"cmp"
	"errors"
	"os"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/providers/gateway"
	"github.com/eriicafes/got"
)

// GetModel returns the model named by MODEL through the AI Gateway, which
// authenticates with AI_GATEWAY_API_KEY or the deployment's OIDC token. The
// gateway reads them on every request, so a cached model picks up rotated
// tokens.
var GetModel = got.TryUsing(func(c *got.Container) (provider.LanguageModel, error) {
	if os.Getenv("AI_GATEWAY_API_KEY") == "" && os.Getenv("VERCEL_OIDC_TOKEN") == "" {
		return nil, errors.New("set AI_GATEWAY_API_KEY, or deploy on Vercel or run vercel env pull for VERCEL_OIDC_TOKEN")
	}
	gw, err := gateway.New(gateway.Config{})
	if err != nil {
		return nil, err
	}
	return gw.LanguageModel(cmp.Or(os.Getenv("MODEL"), "anthropic/claude-sonnet-5.5"))
})
