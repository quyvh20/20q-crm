package ai

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// NewAIGatewayForTest creates an AIGateway with a custom HTTP timeout and
// an explicit gateway URL override. Intended for unit/integration tests only.
//
// Parameters:
//   - gatewayURL: full base URL of the upstream (e.g. a httptest.Server URL)
//   - cfToken:    CF AI token (may be "dummy" in tests)
//   - budget:     pass nil to skip budget enforcement
//   - timeout:    HTTP client timeout for the upstream calls
//   - gwToken:    CF gateway authorization token (may be "dummy")
func NewAIGatewayForTest(gatewayURL, cfToken string, budget *BudgetGuard, timeout time.Duration, gwToken string) *AIGateway {
	return &AIGateway{
		gatewayURL:     gatewayURL,
		cfToken:        cfToken,
		cfGatewayToken: gwToken,
		anthropicKey:   "",
		httpClient:     &http.Client{Timeout: timeout},
		Budget:         budget,
		logger:         zap.NewNop(),
	}
}
