package admin

import (
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
)

func TestResolveEgressUpdateKeepsStoredSources(t *testing.T) {
	current := &config.EgressConfig{Enabled: true, Subscriptions: []config.EgressSubscription{
		{ID: "remote", Name: "remote", URL: "https://token.example/private/feed?key=secret", Enabled: true, RefreshSeconds: 3600},
		{ID: "local", Name: "local", Content: "direct", Enabled: true},
	}}

	// Renaming a remote subscription keeps its stored URL.
	update := &EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{
		{ID: "remote", Name: "renamed", URL: maskSubscriptionURL("https://token.example/private/feed?key=secret"), Enabled: true, RefreshSeconds: 600},
		{ID: "local", Name: "local"},
	}}
	resolved, err := resolveEgressUpdate(update, current)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Subscriptions[0].URL != "https://token.example/private/feed?key=secret" {
		t.Fatalf("url = %q", resolved.Subscriptions[0].URL)
	}
	if resolved.Subscriptions[1].Content != "direct" || resolved.Subscriptions[1].URL != "" {
		t.Fatalf("inline subscription lost its content: %+v", resolved.Subscriptions[1])
	}

	// New inline content replaces the stored URL on purpose.
	switchToInline := &EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{
		{ID: "remote", Content: "http://1.2.3.4:8080#a", Enabled: true},
	}}
	resolved, err = resolveEgressUpdate(switchToInline, current)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Subscriptions[0].URL != "" || resolved.Subscriptions[0].Content != "http://1.2.3.4:8080#a" {
		t.Fatalf("switch to inline = %+v", resolved.Subscriptions[0])
	}

	// A brand-new subscription without any source is rejected.
	missing := &EgressUpdate{Enabled: true, Subscriptions: []EgressSubscriptionUpdate{{ID: "fresh", Name: "fresh"}}}
	if _, err := resolveEgressUpdate(missing, current); err == nil {
		t.Fatal("new subscription must provide a source")
	}
}

func TestEgressViewMasksURLAndReportsInlineLines(t *testing.T) {
	view := egressView(&config.EgressConfig{Enabled: true, Subscriptions: []config.EgressSubscription{
		{ID: "remote", URL: "https://token.example/private/feed?key=secret", Enabled: true},
		{ID: "local", Content: "http://1.1.1.1:8080#a\n\nhttp://2.2.2.2:8080#b\n", Enabled: true},
	}})
	if view.Subscriptions[0].URL != "https://token.example/***" {
		t.Fatalf("masked url = %q", view.Subscriptions[0].URL)
	}
	if !view.Subscriptions[1].Inline || view.Subscriptions[1].ContentLines != 2 {
		t.Fatalf("inline view = %+v", view.Subscriptions[1])
	}
}
