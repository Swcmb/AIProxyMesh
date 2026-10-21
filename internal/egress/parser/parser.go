// Package parser turns subscription payloads into egress nodes. It supports
// plain proxy URI lists, base64-wrapped lists, Clash YAML/JSON proxy arrays and
// sing-box JSON outbounds. Entries whose protocol has no v1 outbound are still
// returned, marked with their raw protocol, so operators can see what a
// subscription contributed instead of silently losing it.
package parser

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress/node"
)

// Node is one parsed subscription entry.
type Node struct {
	Options node.Options
	Raw     json.RawMessage
}

// Stats reports how a payload was interpreted.
type Stats struct {
	Format      string `json:"format"`
	Total       int    `json:"total"`
	Supported   int    `json:"supported"`
	Unsupported int    `json:"unsupported"`
	Duplicates  int    `json:"duplicates"`
	Skipped     int    `json:"skipped"`
}

// Result is the parser output.
type Result struct {
	Nodes []Node
	Stats Stats
}

// ErrEmpty reports a payload with no recognizable proxy entries.
var ErrEmpty = fmt.Errorf("subscription contains no proxy entries")

// Parse interprets subscription bytes and returns deduplicated nodes.
func Parse(data []byte) (Result, error) {
	text := normalizeText(data)
	text = maybeBase64(text)

	if looksLikeCSV(text) {
		if result := parseCSV(text); len(result.Nodes) > 0 {
			return finalize(result, "csv"), nil
		}
	}
	if looksLikeJSON(text) {
		if result, err := parseJSON(text); err == nil && len(result.Nodes) > 0 {
			return finalize(result, "json"), nil
		}
	}
	if looksLikeClashYAML(text) {
		if result := parseClashYAML(text); len(result.Nodes) > 0 {
			return finalize(result, "clash"), nil
		}
	}
	result := parseURILines(text)
	if len(result.Nodes) == 0 {
		return result, ErrEmpty
	}
	return finalize(result, "uri"), nil
}

func finalize(result Result, format string) Result {
	seen := make(map[node.Hash]struct{}, len(result.Nodes))
	out := result.Nodes[:0]
	for _, entry := range result.Nodes {
		h := node.HashOptions(entry.Options)
		if _, exists := seen[h]; exists {
			result.Stats.Duplicates++
			continue
		}
		seen[h] = struct{}{}
		out = append(out, entry)
	}
	result.Nodes = out
	result.Stats.Format = format
	result.Stats.Total = len(out)
	result.Stats.Supported = 0
	result.Stats.Unsupported = 0
	for _, entry := range out {
		if entry.Options.Supported() {
			result.Stats.Supported++
		} else {
			result.Stats.Unsupported++
		}
	}
	return result
}

func normalizeText(data []byte) string {
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func maybeBase64(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || looksLikeJSON(trimmed) || strings.Contains(trimmed, "://") || strings.Contains(trimmed, "proxies:") {
		return text
	}
	if decoded, ok := decodeBase64Relaxed(trimmed); ok {
		if looksLikeJSON(decoded) || strings.Contains(decoded, "://") || strings.Contains(decoded, "proxies:") {
			return decoded
		}
	}
	return text
}

func decodeBase64Relaxed(input string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, input)
	cleaned = strings.NewReplacer("-", "+", "_", "/").Replace(cleaned)
	switch len(cleaned) % 4 {
	case 2:
		cleaned += "=="
	case 3:
		cleaned += "="
	}
	raw, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", false
	}
	out := string(raw)
	if !utf8Printable(out) {
		return "", false
	}
	return out, true
}

func utf8Printable(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func looksLikeJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func looksLikeClashYAML(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "proxies:") {
			return true
		}
	}
	return false
}

// --- URI lines ---

func parseURILines(text string) Result {
	result := Result{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		entry, ok := parseLine(line)
		if !ok {
			result.Stats.Skipped++
			continue
		}
		result.Nodes = append(result.Nodes, entry)
	}
	return result
}

func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if (line[i] == '#' || line[i] == ';') && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
		if line[i] == '/' && i+1 < len(line) && line[i+1] == '/' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func parseLine(line string) (Node, bool) {
	if strings.EqualFold(line, "direct") {
		return newEntry(node.Options{Type: node.Direct, Tag: "direct"})
	}
	if strings.Contains(line, "://") {
		return parseURI(line)
	}
	return parseHostPortLine(line)
}

func parseURI(raw string) (Node, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Node{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	tag := decodeFragment(u.Fragment)
	if scheme == "direct" {
		return newEntry(node.Options{Type: node.Direct, Tag: tag})
	}
	if !isTierAScheme(scheme) {
		return unsupportedFromURL(scheme, u, tag)
	}
	port := u.Port()
	if port == "" {
		return Node{}, false
	}
	portNum, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNum == 0 {
		return Node{}, false
	}
	if u.Path != "" && u.Path != "/" {
		return Node{}, false
	}
	if u.RawQuery != "" {
		return Node{}, false
	}
	opts := node.Options{
		Type:   node.Protocol(scheme),
		Server: u.Hostname(),
		Port:   uint16(portNum),
		Tag:    tag,
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		opts.Password, _ = u.User.Password()
	}
	if opts.Server == "" {
		return Node{}, false
	}
	return newEntry(opts)
}

func isTierAScheme(scheme string) bool {
	switch scheme {
	case "http", "https", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func unsupportedFromURL(scheme string, u *url.URL, tag string) (Node, bool) {
	if scheme == "" {
		return Node{}, false
	}
	opts := node.Options{Type: node.Protocol(scheme), RawType: scheme, Tag: tag, Server: u.Hostname()}
	if port := u.Port(); port != "" {
		if parsed, err := strconv.ParseUint(port, 10, 16); err == nil && parsed > 0 {
			opts.Port = uint16(parsed)
		}
	}
	return newEntry(opts)
}

func decodeFragment(fragment string) string {
	if fragment == "" {
		return ""
	}
	decoded, err := url.PathUnescape(fragment)
	if err != nil {
		return fragment
	}
	return strings.TrimSpace(decoded)
}

func parseHostPortLine(line string) (Node, bool) {
	parts := strings.Split(line, ":")
	switch len(parts) {
	case 2:
		port, ok := parsePort(parts[1])
		if !ok {
			return Node{}, false
		}
		return newEntry(node.Options{Type: node.HTTP, Server: parts[0], Port: port})
	case 4:
		port, ok := parsePort(parts[1])
		if !ok {
			return Node{}, false
		}
		return newEntry(node.Options{Type: node.HTTP, Server: parts[0], Port: port, Username: parts[2], Password: parts[3]})
	default:
		return Node{}, false
	}
}

func parsePort(raw string) (uint16, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 16)
	if err != nil || value == 0 {
		return 0, false
	}
	return uint16(value), true
}

func newEntry(opts node.Options) (Node, bool) {
	opts.Server = strings.TrimSpace(opts.Server)
	if opts.Type != node.Direct && opts.Server == "" {
		return Node{}, false
	}
	if opts.Type == node.Direct {
		opts.Port = 0
	}
	opts.Tag = strings.TrimSpace(opts.Tag)
	if opts.Tag == "" {
		if opts.Type == node.Direct {
			opts.Tag = "direct"
		} else {
			opts.Tag = opts.Addr()
		}
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return Node{}, false
	}
	return Node{Options: opts, Raw: raw}, true
}

// --- JSON ---

func parseJSON(text string) (Result, error) {
	trimmed := strings.TrimSpace(text)
	var outbounds []map[string]any
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &outbounds); err != nil {
			return Result{}, err
		}
	} else {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
			return Result{}, err
		}
		for _, key := range []string{"outbounds", "proxies"} {
			raw, ok := envelope[key]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, &outbounds); err != nil {
				return Result{}, err
			}
			break
		}
	}
	result := Result{}
	for _, item := range outbounds {
		entry, ok := nodeFromMap(item)
		if !ok {
			result.Stats.Skipped++
			continue
		}
		result.Nodes = append(result.Nodes, entry)
	}
	return result, nil
}

// --- Clash YAML (minimal, line-oriented) ---

func parseClashYAML(text string) Result {
	result := Result{}
	lines := strings.Split(text, "\n")
	inSection := false
	baseIndent := 0
	var current map[string]string
	flush := func() {
		if current == nil {
			return
		}
		if entry, ok := nodeFromMap(stringMapToAny(current)); ok {
			result.Nodes = append(result.Nodes, entry)
		} else {
			result.Stats.Skipped++
		}
		current = nil
	}
	for _, rawLine := range lines {
		line := stripComment(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if !inSection {
			if trimmed == "proxies:" {
				inSection = true
				baseIndent = indent
			}
			continue
		}
		if indent <= baseIndent && !strings.HasPrefix(trimmed, "-") {
			flush()
			inSection = false
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			flush()
			current = map[string]string{}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if strings.HasPrefix(item, "{") {
				current = parseFlowMap(item)
				continue
			}
			if key, value, ok := splitKeyValue(item); ok {
				current[key] = value
			}
			continue
		}
		if current == nil {
			continue
		}
		if key, value, ok := splitKeyValue(trimmed); ok {
			current[key] = value
		}
	}
	flush()
	return result
}

func parseFlowMap(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "{")
	raw = strings.TrimSuffix(raw, "}")
	for _, part := range splitComma(raw) {
		if key, value, ok := splitKeyValue(part); ok {
			out[key] = value
		}
	}
	return out
}

func splitComma(raw string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	for _, r := range raw {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
		case r == ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func splitKeyValue(part string) (string, string, bool) {
	idx := strings.Index(part, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(part[:idx]))
	value := unquote(strings.TrimSpace(part[idx+1:]))
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func unquote(value string) string {
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			if decoded, err := strconv.Unquote(value); err == nil {
				return decoded
			}
			return value[1 : len(value)-1]
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
	}
	return value
}

func stringMapToAny(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// --- shared map conversion ---

func nodeFromMap(item map[string]any) (Node, bool) {
	rawType := strings.ToLower(strings.TrimSpace(stringValue(item, "type")))
	name := strings.TrimSpace(firstString(item, "tag", "name"))
	server := strings.TrimSpace(stringValue(item, "server"))
	port := uint16FromAny(firstValue(item, "server_port", "port"))

	tls := false
	switch value := item["tls"].(type) {
	case bool:
		tls = value
	case string:
		tls = strings.EqualFold(value, "true")
	case map[string]any:
		if enabled, ok := value["enabled"].(bool); ok {
			tls = enabled
		}
	}

	opts := node.Options{Server: server, Port: port, Tag: name}
	opts.Username = strings.TrimSpace(stringValue(item, "username"))
	opts.Password = stringValue(item, "password")

	switch rawType {
	case "http":
		if tls {
			opts.Type = node.HTTPS
			opts.TLS = true
		} else {
			opts.Type = node.HTTP
		}
	case "https":
		opts.Type = node.HTTPS
		opts.TLS = true
	case "socks", "socks5", "socks5h":
		opts.Type = node.SOCKS5
		if rawType == "socks5h" {
			opts.Type = node.SOCKS5H
		}
	default:
		if rawType == "" {
			return Node{}, false
		}
		opts.Type = node.Protocol(rawType)
		opts.RawType = rawType
	}
	if opts.Type != node.Direct && (opts.Server == "" || opts.Port == 0) {
		if opts.Supported() {
			return Node{}, false
		}
	}
	return newEntry(opts)
}

func firstValue(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return value
		}
	}
	return nil
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := item[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func stringValue(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func uint16FromAny(value any) uint16 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 && typed <= 65535 {
			return uint16(typed)
		}
	case int:
		if typed > 0 && typed <= 65535 {
			return uint16(typed)
		}
	case json.Number:
		if parsed, err := strconv.ParseUint(string(typed), 10, 16); err == nil {
			return uint16(parsed)
		}
	case string:
		if parsed, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 16); err == nil {
			return uint16(parsed)
		}
	}
	return 0
}

// looksLikeCSV matches the hproxy free-proxy-list header. The CSV source is a
// table, not a proxy list, so it needs its own branch before URI parsing.
func looksLikeCSV(text string) bool {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(line, "ip,port,protocols") || strings.HasPrefix(line, "ip, port, protocols")
}

// parseCSV converts the hproxy table (ip,port,protocols,...,alive) into proxy
// URIs and reuses the URI parser, so dedupe and unsupported handling stay in
// one place.
func parseCSV(text string) Result {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	reader.LazyQuotes = true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		return Result{}
	}
	index := make(map[string]int, len(rows[0]))
	for i, name := range rows[0] {
		index[strings.ToLower(strings.TrimSpace(name))] = i
	}
	ipColumn, hasIP := index["ip"]
	portColumn, hasPort := index["port"]
	protocolColumn, hasProtocol := index["protocols"]
	if !hasIP || !hasPort || !hasProtocol {
		return Result{}
	}
	aliveColumn, hasAlive := index["alive"]

	var lines []string
	skipped := 0
	for _, row := range rows[1:] {
		if hasAlive {
			switch strings.ToLower(strings.TrimSpace(cell(row, aliveColumn))) {
			case "true", "1", "yes":
			default:
				skipped++
				continue
			}
		}
		ip := strings.TrimSpace(cell(row, ipColumn))
		port := strings.TrimSpace(cell(row, portColumn))
		if ip == "" || port == "" {
			skipped++
			continue
		}
		for _, scheme := range csvProtocols(cell(row, protocolColumn)) {
			lines = append(lines, scheme+"://"+net.JoinHostPort(ip, port))
		}
	}
	result := parseURILines(strings.Join(lines, "\n"))
	result.Stats.Skipped += skipped
	return result
}

func cell(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

// csvProtocols returns the schemes this gateway understands. The hproxy field
// may carry several protocols and, depending on the export, may quote them.
func csvProtocols(value string) []string {
	seen := make(map[string]bool, 4)
	var schemes []string
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	}) {
		scheme := strings.ToLower(strings.TrimSpace(field))
		switch scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			continue
		}
		if seen[scheme] {
			continue
		}
		seen[scheme] = true
		schemes = append(schemes, scheme)
	}
	return schemes
}
