package rotation

import (
	"slices"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := New("", config.RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestApplyOrdersGroupsAndStaysSticky(t *testing.T) {
	manager := newManager(t)
	all := map[string]bool{"a": true, "b": true, "c": true}
	manager.Apply(map[string][]string{"sota": {"a", "b", "c"}, "sweet": {"c", "b"}}, all)

	snapshot := manager.Snapshot()
	if snapshot.Groups[0].ID != "sota" || !slices.Equal(snapshot.Groups[0].Order, []string{"a", "b", "c"}) {
		t.Fatalf("sota order = %+v", snapshot.Groups[0])
	}
	if snapshot.Groups[0].Current != "a" {
		t.Fatalf("sota current = %q", snapshot.Groups[0].Current)
	}
	if snapshot.Groups[1].Current != "c" {
		t.Fatalf("sweet current = %q", snapshot.Groups[1].Current)
	}

	// A ranking refresh must not interrupt the model in use.
	manager.Apply(map[string][]string{"sota": {"b", "a", "c"}, "sweet": {"c", "b"}}, all)
	if current := manager.Snapshot().Groups[0].Current; current != "a" {
		t.Fatalf("sota current after reorder = %q, want a", current)
	}

	// Dropping the current model moves to the first usable one.
	manager.Apply(map[string][]string{"sota": {"b", "c"}, "sweet": {"c", "b"}}, all)
	snapshot = manager.Snapshot()
	if snapshot.Groups[0].Current != "b" {
		t.Fatalf("sota current after drop = %q", snapshot.Groups[0].Current)
	}
	if !slices.ContainsFunc(snapshot.Groups[0].Switches, func(s Switch) bool { return s.To == "b" }) {
		t.Fatalf("switch not recorded: %+v", snapshot.Groups[0].Switches)
	}
}

func TestApplyIgnoresUnavailableAndDuplicateModels(t *testing.T) {
	manager := newManager(t)
	manager.Apply(map[string][]string{"sota": {"a", "a", "b"}, "sweet": {"a"}}, map[string]bool{"b": true})
	snapshot := manager.Snapshot()
	if !slices.Equal(snapshot.Groups[0].Order, []string{"a", "b"}) {
		t.Fatalf("order = %v", snapshot.Groups[0].Order)
	}
	if snapshot.Groups[0].Available["a"] {
		t.Fatal("unranked-as-unavailable model must stay unusable")
	}
	if snapshot.Groups[0].Current != "b" {
		t.Fatalf("current = %q, want b", snapshot.Groups[0].Current)
	}
	if snapshot.Groups[1].Current != "" || len(snapshot.Groups[1].Order) != 1 {
		t.Fatalf("sweet group = %+v", snapshot.Groups[1])
	}
	if _, ok := manager.Select("sota", map[string]bool{"b": true}); ok {
		t.Fatal("only b is usable and it was visited")
	}
	token, ok := manager.Select("sota", nil)
	if !ok || token.Model != "b" {
		t.Fatalf("select = %+v %v", token, ok)
	}
}

func TestConfigureKeepsRankingOrder(t *testing.T) {
	manager := newManager(t)
	manager.Apply(map[string][]string{"sota": {"a", "b"}, "sweet": {"b"}}, map[string]bool{"a": true, "b": true})
	manager.Configure(config.RotationConfig{
		SOTA:  config.RotationGroup{Alias: "fast"},
		Sweet: config.RotationGroup{Alias: "cheap"},
	})
	snapshot := manager.Snapshot()
	if snapshot.Groups[0].Alias != "fast" || snapshot.Groups[1].Alias != "cheap" {
		t.Fatalf("aliases = %+v", snapshot.Groups)
	}
	if !slices.Equal(snapshot.Groups[0].Order, []string{"a", "b"}) {
		t.Fatalf("configure reset the ranking order: %v", snapshot.Groups[0].Order)
	}
	if snapshot.FirstOutputSeconds != 30 {
		t.Fatalf("first output = %d", snapshot.FirstOutputSeconds)
	}
}
