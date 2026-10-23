package node

import (
	"fmt"
	"net"
	"strings"
)

// ClusterKey groups exits that share a source network so exploration can
// sample one representative instead of every port in the same block. IPv4
// groups by /24 and IPv6 by /48; hostnames and unparsable servers return ""
// and are simply not clustered.
func ClusterKey(server string) string {
	ip := net.ParseIP(strings.TrimSpace(server))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("v4:%d.%d.%d", v4[0], v4[1], v4[2])
	}
	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	return fmt.Sprintf("v6:%02x%02x:%02x%02x:%02x%02x", v6[0], v6[1], v6[2], v6[3], v6[4], v6[5])
}

// ClusterKeyFor returns the cluster of an entry's endpoint.
func (e *Entry) ClusterKeyFor() string {
	if e == nil {
		return ""
	}
	return ClusterKey(e.Options.Server)
}
