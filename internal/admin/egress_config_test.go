package admin

import (
	"strings"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
)

func TestEgressViewMasksSubscriptionURL(t *testing.T) {
	cfg := &config.EgressConfig{
		Enabled: true,
		Subscriptions: []config.EgressSubscription{{
			ID: "sub", Name: "sub", URL: "https://user:pass@example.com/secret-token?key=abc", Enabled: true, RefreshSeconds: 3600,
		}},
	}
	view := egressView(cfg)
	if view == nil || len(view.Subscriptions) != 1 {
		t.Fatalf("view = %+v", view)
	}
	masked := view.Subscriptions[0].URL
	if strings.Contains(masked, "secret-token") || strings.Contains(masked, "user") || strings.Contains(masked, "abc") {
		t.Fatalf("masked url leaks secrets: %q", masked)
	}
	if !strings.HasPrefix(masked, "https://example.com/") {
		t.Fatalf("masked url = %q", masked)
	}
}

func TestResolveEgressUpdateKeepsMaskedURL(t *testing.T) {
	current := &config.EgressConfig{
		Enabled: true,
		Subscriptions: []config.EgressSubscription{{
			ID: "sub", Name: "old", URL: "https://example.com/token", Enabled: true, RefreshSeconds: 3600,
		}},
	}
	update := &EgressUpdate{
		Enabled: true,
		Subscriptions: []EgressSubscriptionUpdate{{
			ID: "sub", Name: "new", URL: maskSubscriptionURL("https://example.com/token"), Enabled: false, RefreshSeconds: 60,
		}},
	}
	resolved, err := resolveEgressUpdate(update, current)
	if err != nil {
		t.Fatal(err)
	}
	sub := resolved.Subscriptions[0]
	if sub.URL != "https://example.com/token" {
		t.Fatalf("url = %q", sub.URL)
	}
	if sub.Name != "new" || sub.Enabled || sub.RefreshSeconds != 60 {
		t.Fatalf("sub = %+v", sub)
	}
}

func TestResolveEgressUpdateValidation(t *testing.T) {
	current := &config.EgressConfig{}
	if _, err := resolveEgressUpdate(&EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{{ID: "new"}}}, current); err == nil {
		t.Fatal("new subscription without url must fail")
	}
	duplicate := &EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{
		{ID: "a", URL: "https://x.test/sub"},
		{ID: "a", URL: "https://y.test/sub"},
	}}
	if _, err := resolveEgressUpdate(duplicate, current); err == nil {
		t.Fatal("duplicate ids must fail")
	}
	if got, err := resolveEgressUpdate(nil, current); err != nil || got != current {
		t.Fatal("nil update must preserve the current section")
	}
}

func TestResolveEgressUpdateReplacesSubscriptions(t *testing.T) {
	current := &config.EgressConfig{Subscriptions: []config.EgressSubscription{
		{ID: "a", URL: "https://a.test/sub"}, {ID: "b", URL: "https://b.test/sub"},
	}}
	update := &EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{{ID: "b", URL: "https://b.test/new"}}}
	resolved, err := resolveEgressUpdate(update, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Subscriptions) != 1 || resolved.Subscriptions[0].URL != "https://b.test/new" {
		t.Fatalf("subscriptions = %+v", resolved.Subscriptions)
	}
}

func TestEgressGeoIPRoundTrip(t *testing.T) {
	enabled := true
	geo := &config.EgressGeoIP{Enabled: &enabled, URL: "https://mirror.test/country.mmdb", RefreshDays: 7}
	cfg := &config.EgressConfig{Enabled: true, GeoIP: geo}
	view := egressView(cfg)
	if view.GeoIP == nil || view.GeoIP.URL != geo.URL || view.GeoIP.RefreshDays != 7 {
		t.Fatalf("view must expose geoip, got %+v", view.GeoIP)
	}
	resolved, err := resolveEgressUpdate(&EgressUpdate{Enabled: true, GeoIP: geo}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.GeoIP == nil || resolved.GeoIP.URL != geo.URL {
		t.Fatalf("update must carry geoip, got %+v", resolved.GeoIP)
	}
}
