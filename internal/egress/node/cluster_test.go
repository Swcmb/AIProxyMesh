package node

import "testing"

func TestClusterKeyGroupsSourceNetworks(t *testing.T) {
	cases := []struct {
		server string
		want   string
	}{
		{"144.124.251.24", "v4:144.124.251"},
		{"144.124.251.99", "v4:144.124.251"},
		{"144.124.252.24", "v4:144.124.252"},
		{"2001:db8:abcd:12::1", "v6:2001:0db8:abcd"},
		{"2001:db8:abcd:ffff::2", "v6:2001:0db8:abcd"},
		{"proxy.example.com", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := ClusterKey(tc.server); got != tc.want {
			t.Fatalf("ClusterKey(%q) = %q, want %q", tc.server, got, tc.want)
		}
	}
}
