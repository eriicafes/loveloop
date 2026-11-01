package flows

import (
	"cmp"
	"errors"
	"os"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/providers/anthropic"
	"github.com/digitallysavvy/go-ai/pkg/providers/gateway"
	"github.com/eriicafes/got"
)

// GetModel returns the model named by MODEL through the AI Gateway, which
// authenticates with AI_GATEWAY_API_KEY or, on Vercel, the deployment's OIDC
// token. With only ANTHROPIC_API_KEY set it calls Anthropic directly. The
// gateway reads the OIDC token on every request, so a cached model picks up
// rotated tokens.
var GetModel = got.TryUsing(func(c *got.Container) (provider.LanguageModel, error) {
	if os.Getenv("AI_GATEWAY_API_KEY") == "" {
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			return anthropic.New(anthropic.Config{APIKey: key}).LanguageModel(cmp.Or(os.Getenv("MODEL"), anthropic.ClaudeSonnet5_5))
		}
		if os.Getenv("VERCEL_OIDC_TOKEN") == "" {
			return nil, errors.New("set AI_GATEWAY_API_KEY or ANTHROPIC_API_KEY, or deploy on Vercel for OIDC")
		}
	}
	gw, err := gateway.New(gateway.Config{})
	if err != nil {
		return nil, err
	}
	return gw.LanguageModel(cmp.Or(os.Getenv("MODEL"), "anthropic/claude-sonnet-5.5"))
})
