package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/routing"
)

func TestClassifyNotFound(t *testing.T) {
	outcome := classifyEgressOutcome(context.Background(), &http.Response{StatusCode: http.StatusNotFound}, nil, false, true)
	if outcome.Kind != routing.NotFound {
		t.Fatalf("kind = %s", outcome.Kind)
	}
}
