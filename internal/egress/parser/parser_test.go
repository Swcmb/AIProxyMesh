package parser

import (
	"encoding/base64"
	"testing"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

func byName(result Result) map[string]node.Options {
	out := make(map[string]node.Options, len(result.Nodes))
	for _, entry := range result.Nodes {
		out[entry.Options.Tag] = entry.Options
	}
	return out
}

func TestParseURILines(t *testing.T) {
	payload := `# comment
direct
http://user:p%40ss@1.2.3.4:8080#edge-a
https://5.6.7.8:443
socks5://9.9.9.9:1080
socks5h://9.9.9.9:1081#remote-dns
10.0.0.1:3128
10.0.0.2:3129:bob:secret
unsupported://example.com:1234#odd
`
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Format != "uri" || result.Stats.Total != 8 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	nodes := byName(result)
	if nodes["edge-a"].Type != node.HTTP || nodes["edge-a"].Username != "user" || nodes["edge-a"].Password != "p@ss" {
		t.Fatalf("userinfo not decoded: %+v", nodes["edge-a"])
	}
	if nodes["remote-dns"].Type != node.SOCKS5H {
		t.Fatalf("socks5h lost: %+v", nodes["remote-dns"])
	}
	if nodes["10.0.0.2:3129"].Password != "secret" {
		t.Fatalf("host:port:user:pass not parsed: %+v", nodes["10.0.0.2:3129"])
	}
	if nodes["odd"].Supported() {
		t.Fatal("unknown scheme must be unsupported")
	}
	if result.Stats.Unsupported != 1 || result.Stats.Supported != 7 {
		t.Fatalf("supported split = %+v", result.Stats)
	}
}

func TestParseBase64(t *testing.T) {
	inner := "http://u:p@1.1.1.1:8080#wrapped\nsocks5://2.2.2.2:1080\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(inner))
	result, err := Parse([]byte("\n  " + encoded + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Total != 2 {
		t.Fatalf("decoded nodes = %d", result.Stats.Total)
	}
	if byName(result)["wrapped"].Password != "p" {
		t.Fatal("base64 payload not decoded")
	}
}

func TestParseDeduplicates(t *testing.T) {
	payload := "http://1.1.1.1:80#a\nhttp://1.1.1.1:80#b\n"
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Total != 1 || result.Stats.Duplicates != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
}

func TestParseRejectsAmbiguousPaths(t *testing.T) {
	result, err := Parse([]byte("http://1.1.1.1:80/path\nhttp://1.1.1.1:80?x=1\nhttp://1.1.1.1\n"))
	if err == nil {
		t.Fatalf("expected empty result error, got %+v", result)
	}
	if result.Stats.Skipped != 3 {
		t.Fatalf("skipped = %d", result.Stats.Skipped)
	}
}

func TestParseClashYAML(t *testing.T) {
	payload := `mixed-port: 7890
proxies:
  - name: "yaml-http"
    type: http
    server: 1.2.3.4
    port: 8080
    username: alice
    password: "p#ss"
    tls: true
  - {name: yaml-socks, type: socks5, server: 5.6.7.8, port: 1080}
  - name: yaml-ss
    type: ss
    server: 9.9.9.9
    port: 8388
proxy-groups:
  - name: auto
    type: url-test
`
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Format != "clash" || result.Stats.Total != 3 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	nodes := byName(result)
	if nodes["yaml-http"].Type != node.HTTPS || nodes["yaml-http"].Password != "p#ss" || nodes["yaml-http"].Username != "alice" {
		t.Fatalf("clash block entry = %+v", nodes["yaml-http"])
	}
	if nodes["yaml-socks"].Type != node.SOCKS5 {
		t.Fatalf("clash flow entry = %+v", nodes["yaml-socks"])
	}
	if nodes["yaml-ss"].Supported() || nodes["yaml-ss"].RawType != "ss" {
		t.Fatalf("unsupported clash entry = %+v", nodes["yaml-ss"])
	}
}

func TestParseSingboxJSON(t *testing.T) {
	payload := `{
	  "outbounds": [
	    {"type":"http","tag":"json-http","server":"1.2.3.4","server_port":8080,"username":"u","password":"p"},
	    {"type":"socks","tag":"json-socks","server":"5.6.7.8","server_port":1080},
	    {"type":"http","tag":"json-tls","server":"9.9.9.9","server_port":443,"tls":{"enabled":true}},
	    {"type":"vless","tag":"json-vless","server":"8.8.8.8","server_port":443}
	  ]
	}`
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Format != "json" || result.Stats.Total != 4 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	nodes := byName(result)
	if nodes["json-http"].Type != node.HTTP || nodes["json-http"].Username != "u" {
		t.Fatalf("http entry = %+v", nodes["json-http"])
	}
	if nodes["json-socks"].Type != node.SOCKS5 {
		t.Fatalf("socks entry = %+v", nodes["json-socks"])
	}
	if nodes["json-tls"].Type != node.HTTPS {
		t.Fatalf("tls entry = %+v", nodes["json-tls"])
	}
	if nodes["json-vless"].Supported() {
		t.Fatalf("vless must stay unsupported: %+v", nodes["json-vless"])
	}
}

func TestParseClashJSON(t *testing.T) {
	payload := `{"proxies":[{"name":"c1","type":"socks5","server":"1.1.1.1","port":1080}]}`
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Options.Type != node.SOCKS5 {
		t.Fatalf("clash json = %+v", result.Nodes)
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse([]byte("  \n# only comments\n")); err != ErrEmpty {
		t.Fatalf("expected ErrEmpty, got %v", err)
	}
}

func TestParseHproxyCSV(t *testing.T) {
	payload := `ip,port,protocols,anonymity,country,city,latency_ms,uptime_pct,alive
1.2.3.4,8080,http,anonymous,US,Ashburn,10,99,true
5.6.7.8,1080,"socks5,http",transparent,DE,Berlin,20,90,true
9.9.9.9,3128,https,anonymous,FR,Paris,30,80,false
10.0.0.1,3129,vmess,anonymous,JP,Tokyo,40,70,true
`
	result, err := Parse([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Format != "csv" {
		t.Fatalf("format = %s", result.Stats.Format)
	}
	// Three live protocols survive (http, socks5, http); the dead row and the
	// protocol-less vmess row contribute nothing.
	if result.Stats.Total != 3 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	var http, socks int
	for _, entry := range result.Nodes {
		switch entry.Options.Type {
		case node.HTTP:
			http++
			if entry.Options.Server != "1.2.3.4" && entry.Options.Server != "5.6.7.8" {
				t.Fatalf("unexpected server %q", entry.Options.Server)
			}
		case node.SOCKS5:
			socks++
			if entry.Options.Server != "5.6.7.8" || entry.Options.Port != 1080 {
				t.Fatalf("unexpected socks node %+v", entry.Options)
			}
		}
	}
	if http != 2 || socks != 1 {
		t.Fatalf("http=%d socks=%d", http, socks)
	}
}
