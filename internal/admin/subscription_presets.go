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
// Both are restricted to HTTP/HTTPS: SOCKS exits in public lists are rarely
// stable and this gateway already prefers plain HTTP tunnels for them.
var subscriptionPresets = []subscriptionPreset{
	{
		ID:        "api-proxy-pool-hproxy",
		Name:      "api-proxy-pool · hproxy",
		URL:       "https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.csv",
		Format:    "csv",
		Protocols: []string{"http", "https"},
		Description: "hproxy 免费代理清单（CSV），只导入存活且协议为 HTTP/HTTPS 的条目，" +
			"按 ip、端口与协议去重。",
		Risk: presetRisk,
	},
	{
		ID:        "api-proxy-pool-proxifly",
		Name:      "api-proxy-pool · proxifly",
		URL:       "https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt",
		Format:    "uri",
		Protocols: []string{"http", "https"},
		Description: "proxifly 免费代理清单（每行一条 URI），只保留 HTTP/HTTPS，" +
			"SOCKS 与其他协议会被过滤。",
		Risk: presetRisk,
	},
}

func (a *Server) handleSubscriptionPresets(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"presets": subscriptionPresets})
}
