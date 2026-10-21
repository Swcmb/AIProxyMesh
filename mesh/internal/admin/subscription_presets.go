package admin

import (
	"net/http"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

// subscriptionPreset is one built-in source offered on the subscriptions page.
// Presets are never applied automatically: the operator adds one explicitly.
type subscriptionPreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Format      string `json:"format"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

var subscriptionPresets = []subscriptionPreset{
	{
		ID:     "api-proxy-pool",
		Name:   "api-proxy-pool 公共代理池",
		URL:    "https://swcmb.github.io/api-proxy-pool/alive.txt",
		Format: "uri",
		Description: "由 GitHub Actions 每日筛选的公开 HTTP/SOCKS 代理列表，" +
			"每行一条代理 URL，刷新后按配置的协议过滤。",
		Risk: "公共代理，可用性与隐私风险高于自建出口；启用前请确认可接受，不建议作为唯一出口。",
	},
}

func (a *Server) handleSubscriptionPresets(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"presets": subscriptionPresets})
}
