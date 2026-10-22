package routing

import (
	"encoding/json"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/pool"
)

func TestDiagnoseSummaryBoundsAndTallies(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	addNode(p, "sub", httpExit("127.0.0.1", 18081, "a"))
	addNode(p, "sub", httpExit("127.0.0.1", 18082, "b"))
	fresh := httpExit("127.0.0.1", 18083, "fresh")
	freshRaw, _ := json.Marshal(fresh)
	p.Add("sub", pool.Input{Options: fresh, Raw: freshRaw})
	disabled := addNode(p, "sub", httpExit("127.0.0.1", 18084, "off"))
	if err := p.SetEnabled(disabled.Hash, false); err != nil {
		t.Fatal(err)
	}

	summary := r.DiagnoseSummary(u, nil, 1)
	if summary.Total != 4 {
		t.Fatalf("total = %d, want the whole pool counted", summary.Total)
	}
	if summary.Eligible != 2 {
		t.Fatalf("eligible = %d, want 2", summary.Eligible)
	}
	if len(summary.Candidates) != 1 {
		t.Fatalf("candidates = %d, want the limit applied", len(summary.Candidates))
	}
	if summary.Reasons["unprobed"] != 1 || summary.Reasons["disabled"] != 1 {
		t.Fatalf("reasons = %v, want ineligible nodes tallied by reason", summary.Reasons)
	}
	if summary.Reasons["eligible"] != 0 {
		t.Fatalf("reasons = %v, eligible nodes must not appear in the histogram", summary.Reasons)
	}

	all := r.DiagnoseSummary(u, nil, 0)
	if len(all.Candidates) != 2 {
		t.Fatalf("candidates = %d, want the default limit to keep both eligible nodes", len(all.Candidates))
	}
	for i := 1; i < len(all.Candidates); i++ {
		if all.Candidates[i-1].Score.Total < all.Candidates[i].Score.Total {
			t.Fatalf("candidates are not ranked by score: %+v", all.Candidates)
		}
	}
}

func TestDiagnoseEntryMatchesBulkScan(t *testing.T) {
	r, p := testRouter(t)
	u := mustURL(t, "https://api.example.com/v1")
	entry := addNode(p, "sub", httpExit("127.0.0.1", 18085, "single"))
	other := httpExit("127.0.0.1", 18086, "other")
	otherRaw, _ := json.Marshal(other)
	p.Add("sub", pool.Input{Options: other, Raw: otherRaw})

	single := r.DiagnoseEntry(u, entry)
	candidates := r.Diagnose(u, nil)
	var bulk *Candidate
	for i, candidate := range candidates {
		if candidate.ID == entry.ID() {
			bulk = &candidates[i]
			break
		}
	}
	if bulk == nil {
		t.Fatal("the node is missing from the bulk scan")
	}
	if single.Eligible != bulk.Eligible || single.Reason != bulk.Reason || single.Score != bulk.Score {
		t.Fatalf("single-node verdict %+v differs from the bulk scan %+v", single, bulk)
	}
}
