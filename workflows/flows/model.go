package flows

import (
	"cmp"
	"errors"
	"net/http"
	"os"

	"github.com/digitallysavvy/go-ai/pkg/provider"
	"github.com/digitallysavvy/go-ai/pkg/providers/openai"
	"github.com/eriicafes/got"
)

// gatewayURL is the AI Gateway's OpenAI-compatible API. go-ai's gateway
// provider sends an outdated request body that the gateway rejects with
// "Invalid input", so LoveLoop talks to the gateway through this API instead.
const gatewayURL = "https://ai-gateway.vercel.sh/v1"

// GetModel returns the model named by MODEL through the AI Gateway's
// OpenAI-compatible Chat Completions API, which authenticates with
// AI_GATEWAY_API_KEY or the deployment's OIDC token.
var GetModel = got.TryUsing(func(c *got.Container) (provider.LanguageModel, error) {
	if gatewayToken() == "" {
		return nil, errors.New("set AI_GATEWAY_API_KEY, or deploy on Vercel or run vercel env pull for VERCEL_OIDC_TOKEN")
	}
	gw := openai.New(openai.Config{
		Name:    "gateway",
		BaseURL: gatewayURL,
		// The transport sets the real token on each request.
		APIKey:     "gateway",
		HTTPClient: &http.Client{Transport: gatewayAuth{next: http.DefaultTransport}},
	})
	return gw.ChatModel(cmp.Or(os.Getenv("MODEL"), "anthropic/claude-sonnet-5.5"))
})

// gatewayToken returns AI_GATEWAY_API_KEY, or else the OIDC token Vercel
// rotates, read anew on every call.
func gatewayToken() string {
	return cmp.Or(os.Getenv("AI_GATEWAY_API_KEY"), os.Getenv("VERCEL_OIDC_TOKEN"))
}

// gatewayAuth sends each request with the current gateway token, so a
// cached model keeps working after Vercel rotates the OIDC token.
type gatewayAuth struct{ next http.RoundTripper }

func (t gatewayAuth) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+gatewayToken())
	return t.next.RoundTrip(request)
}
