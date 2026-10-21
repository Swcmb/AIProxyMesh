package admin

import (
	"net/http"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/httpx"
)

// subscriptionPreset is one built-in source offered on the subscriptions page.
// Presets are never applied automatically: the operator adds one explicitly.
type subscriptionPreset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Format      string   `json:"format"`
	Protocols   []string `json:"protocols"`
	Description string   `json:"description"`
	Risk        string   `json:"risk"`
}

const presetRisk = "公共代理，可用性与隐私风险高于自建出口；启用前请确认可接受，不建议作为唯一出口。"

// subscriptionPresets mirror the two public sources api-proxy-pool aggregates.
// Both import HTTP, HTTPS and the SOCKS5 family; SOCKS4 entries are dropped.
var subscriptionPresets = []subscriptionPreset{
	{
		ID:          "api-proxy-pool-hproxy",
		Name:        "api-proxy-pool · hproxy",
		URL:         "https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.csv",
		Format:      "csv",
		Protocols:   []string{"http", "https", "socks5", "socks5h"},
		Description: "hproxy 免费代理清单",
		Risk:        presetRisk,
	},
	{
		ID:          "api-proxy-pool-proxifly",
		Name:        "api-proxy-pool · proxifly",
		URL:         "https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt",
		Format:      "uri",
		Protocols:   []string{"http", "https", "socks5", "socks5h"},
		Description: "proxifly 免费代理清单",
		Risk:        presetRisk,
	},
}

func (a *Server) handleSubscriptionPresets(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"presets": subscriptionPresets})
}
