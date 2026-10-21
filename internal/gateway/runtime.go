package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swcmb/AIProxyMesh/mesh/internal/config"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/egress"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/logstore"
	modelcatalog "github.com/Swcmb/AIProxyMesh/mesh/internal/models"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/protocol"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/rotation"
	"github.com/Swcmb/AIProxyMesh/mesh/internal/telemetry"
)

type gatewayRuntime struct {
	availability *modelcatalog.AvailabilityStore
	config       config.Config
	gateway      *Gateway
	handler      http.Handler
	cancel       context.CancelFunc
}

type ApplyResult struct {
	Applied         bool     `json:"applied"`
	RestartRequired bool     `json:"restart_required"`
	RestartFields   []string `json:"restart_fields,omitempty"`
}

type RuntimeManager struct {
	rotation           *rotation.Manager
	configPath         string
	root               context.Context
	logger             *slog.Logger
	monitor            *telemetry.Monitor
	hub                *telemetry.LogHub
	redactor           *config.SecretRedactor
	level              *slog.LevelVar
	current            atomic.Pointer[gatewayRuntime]
	updateMu           sync.Mutex
	effective          effectiveListeners
	metadata           *modelcatalog.PricingStore
	availabilityCancel context.CancelFunc
	egressMu           sync.Mutex
	egressEngine       *egress.Engine
	requestLogsMu      sync.Mutex
	requestLogs        *logstore.Service
	requestLogSettings config.RequestsLogConfig
}

type effectiveListeners struct {
	API          string
	WebUI        string
	WebUIEnabled bool
}

func NewRuntimeManager(root context.Context, configPath string, cfg config.Config, logger *slog.Logger, monitor *telemetry.Monitor, hub *telemetry.LogHub, redactor *config.SecretRedactor, level *slog.LevelVar) (*RuntimeManager, error) {
	manager := &RuntimeManager{
		configPath: configPath, root: root, logger: logger, monitor: monitor, hub: hub, redactor: redactor, level: level,
		effective: effectiveListeners{API: cfg.Listen, WebUI: cfg.WebUI.Listen, WebUIEnabled: cfg.WebUI.Enabled},
	}
	manager.metadata = modelcatalog.NewPricingStore(configPath, logger)
	// models.dev refreshes ride the active runtime's healthy proxy transports
	// and fall back to the store's direct client when none are available.
	manager.metadata.SetClientProvider(func() []*http.Client {
		runtime := manager.current.Load()
		if runtime == nil || runtime.gateway == nil {
			return nil
		}
		return runtime.gateway.discoveryClients()
	})
	if cfg.WebUI.Password != "" {
		hash, err := config.HashPassword(cfg.WebUI.Password)
		if err != nil {
			return nil, fmt.Errorf("hash webui password: %w", err)
		}
		cfg.WebUI.PasswordHash = hash
		cfg.WebUI.Password = ""
		if err := config.SaveAtomic(configPath, cfg); err != nil {
			return nil, fmt.Errorf("persist webui password migration: %w", err)
		}
		// The automatically-created backup contains the one-time plaintext
		// bootstrap password and must not be retained.
		_ = os.Remove(configPath + ".bak")
	}
	cfg.Rotation.Defaults()
	rotationManager, err := rotation.New(configPath+".rotation.json", cfg.Rotation)
	if err != nil {
		return nil, err
	}
	manager.rotation = rotationManager
	manager.syncRequestLogs(cfg)
	if err := manager.ensureEgress(cfg); err != nil {
		return nil, err
	}
	runtime, err := manager.build(cfg)
	if err != nil {
		return nil, err
	}
	if err := runtime.gateway.catalog.LoadCache(modelcatalog.CatalogCachePath(configPath)); err != nil && !os.IsNotExist(err) {
		if manager.logger != nil {
			manager.logger.Warn("model catalog cache ignored", "component", "models", "event", "catalog_cache_load_failed", "path", modelcatalog.CatalogCachePath(configPath), "error", err)
		}
	}
	if err := cfg.Rotation.Validate(runtime.gateway.catalog.List()); err != nil {
		return nil, err
	}
	runtime.gateway.syncRotation()
	manager.current.Store(runtime)
	manager.redactor.Replace(cfg)
	telemetry.SetLogLevel(manager.level, cfg.Logging.Level)
	manager.start(runtime)
	manager.metadata.Start(root)
	manager.startAvailabilityChecks()
	return manager, nil
}

func (m *RuntimeManager) build(cfg config.Config) (*gatewayRuntime, error) {
	gateway, err := New(cfg, m.logger, m.monitor)
	if err != nil {
		return nil, err
	}
	gateway.SetEgress(m.Egress())
	gateway.rotation = m.rotation
	availabilityPath := ""
	if m.configPath != "" {
		availabilityPath = m.configPath + "." + config.Fingerprint(cfg.Upstream.Zen) + ".availability.json"
	}
	availability, err := modelcatalog.NewAvailabilityStore(availabilityPath)
	if err != nil {
		return nil, fmt.Errorf("load model availability: %w", err)
	}
	if current := m.current.Load(); current != nil && current.config.Upstream.Zen == cfg.Upstream.Zen {
		availability = current.availability
	}
	gateway.catalog.SetPricingStore(m.metadata)
	gateway.catalog.SetAvailabilityStore(availability)
	gateway.availability = availability
	gateway.catalog.SetCachePath(modelcatalog.CatalogCachePath(m.configPath))
	return &gatewayRuntime{config: cfg, gateway: gateway, availability: availability, handler: gateway.Handler(), cancel: func() {}}, nil
}

func (m *RuntimeManager) start(runtime *gatewayRuntime) {
	// Store the context through a lightweight wrapper so cancel stops catalog
	// and proxy checks while in-flight HTTP requests continue on the old pools.
	runtimeCtx, cancel := context.WithCancel(m.root)
	runtime.cancel = cancel
	runtime.gateway.StartProxyHealthChecks(runtimeCtx)
	runtime.gateway.StartModelRefresh(runtimeCtx)
}

func (m *RuntimeManager) Handler() http.Handler {
	dynamic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtime := m.current.Load()
		if runtime == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		runtime.handler.ServeHTTP(w, r)
	})
	return telemetry.Middleware(m.monitor, m.logger, dynamic)
}

// Redact removes configured credentials from diagnostic output.
func (m *RuntimeManager) Redact(value string) string {
	return m.redactor.String(value)
}

func (m *RuntimeManager) Config() config.Config {
	runtime := m.current.Load()
	if runtime == nil {
		return config.Config{}
	}
	return config.Clone(runtime.config)
}

func (m *RuntimeManager) RestartStatus() (effectiveListeners, []string) {
	cfg := m.Config()
	fields := make([]string, 0, 3)
	if cfg.Listen != m.effective.API {
		fields = append(fields, "listen")
	}
	if cfg.WebUI.Listen != m.effective.WebUI {
		fields = append(fields, "webui.listen")
	}
	if cfg.WebUI.Enabled != m.effective.WebUIEnabled {
		fields = append(fields, "webui.enabled")
	}
	return m.effective, fields
}

func (m *RuntimeManager) Apply(candidate config.Config, persist bool) (ApplyResult, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	return m.applyLocked(candidate, persist)
}

// Update serializes read-modify-write operations against the latest config.
func (m *RuntimeManager) Update(change func(*config.Config) error) (ApplyResult, error) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	candidate := m.Config()
	if err := change(&candidate); err != nil {
		return ApplyResult{}, err
	}
	return m.applyLocked(candidate, true)
}

func (m *RuntimeManager) applyLocked(candidate config.Config, persist bool) (ApplyResult, error) {

	current := m.current.Load()
	hadPlaintextPassword := candidate.WebUI.Password != ""
	if hadPlaintextPassword {
		hash, err := config.HashPassword(candidate.WebUI.Password)
		if err != nil {
			return ApplyResult{}, err
		}
		candidate.WebUI.PasswordHash = hash
		candidate.WebUI.Password = ""
	}
	normalized, err := config.Normalize(m.configPath, candidate)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := m.ensureEgress(normalized); err != nil {
		return ApplyResult{}, err
	}
	m.syncRequestLogs(normalized)
	next, err := m.build(normalized)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("initialize runtime: %w", err)
	}
	if current != nil {
		next.gateway.catalog.CopyState(current.gateway.catalog)
	}
	if err := normalized.Rotation.Validate(next.gateway.catalog.List()); err != nil {
		return ApplyResult{}, err
	}
	if persist || hadPlaintextPassword {
		if err := config.SaveAtomic(m.configPath, normalized); err != nil {
			next.cancel()
			return ApplyResult{}, err
		}
	}

	result := ApplyResult{Applied: true}
	if normalized.Listen != m.effective.API {
		result.RestartFields = append(result.RestartFields, "listen")
	}
	if normalized.WebUI.Listen != m.effective.WebUI {
		result.RestartFields = append(result.RestartFields, "webui.listen")
	}
	if normalized.WebUI.Enabled != m.effective.WebUIEnabled {
		result.RestartFields = append(result.RestartFields, "webui.enabled")
	}
	result.RestartRequired = len(result.RestartFields) > 0
	m.redactor.Replace(normalized)
	telemetry.SetLogLevel(m.level, normalized.Logging.Level)
	if current == nil || normalized.Logging.RingSize != current.config.Logging.RingSize {
		m.hub.Resize(normalized.Logging.RingSize)
	}
	m.rotation.Configure(normalized.Rotation)
	next.gateway.syncRotation()
	m.start(next)
	previous := m.current.Swap(next)
	if previous != nil {
		previous.cancel()
	}
	m.logger.Info("configuration applied", "component", "config", "event", "config_applied", "restart_required", result.RestartRequired, "restart_fields", result.RestartFields)
	return result, nil
}

func (m *RuntimeManager) Reload() (ApplyResult, error) {
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return ApplyResult{}, err
	}
	hadPlaintextPassword := cfg.WebUI.Password != ""
	result, err := m.Apply(cfg, false)
	if err == nil && hadPlaintextPassword {
		_ = os.Remove(m.configPath + ".bak")
	}
	return result, err
}

func (m *RuntimeManager) Shutdown() {
	if m.availabilityCancel != nil {
		m.availabilityCancel()
	}
	m.egressMu.Lock()
	if m.egressEngine != nil {
		_ = m.egressEngine.Close()
		m.egressEngine = nil
	}
	m.egressMu.Unlock()
	if current := m.current.Load(); current != nil {
		current.cancel()
	}
	m.requestLogsMu.Lock()
	defer m.requestLogsMu.Unlock()
	_ = m.requestLogs.Close()
	m.requestLogs = nil
	m.monitor.SetSink(nil)
}

// Egress returns the exit-pool engine, or nil when the feature is disabled.
func (m *RuntimeManager) Egress() *egress.Engine {
	m.egressMu.Lock()
	defer m.egressMu.Unlock()
	return m.egressEngine
}

// RequestLogs returns the persistent request log, or nil when persistence is off.
func (m *RuntimeManager) RequestLogs() *logstore.Service {
	m.requestLogsMu.Lock()
	defer m.requestLogsMu.Unlock()
	return m.requestLogs
}

// syncRequestLogs reconciles the persistent request log with the configuration.
// Only the enable flag and the database path need a reopen; the retention window
// and row cap are pushed to the running writer.
func (m *RuntimeManager) syncRequestLogs(cfg config.Config) {
	m.requestLogsMu.Lock()
	defer m.requestLogsMu.Unlock()
	requests := cfg.Logging.Requests
	if m.requestLogs != nil && m.requestLogSettings.SameSourceAs(requests) {
		m.requestLogs.SetRetention(requests.RetentionDays, requests.MaxRows)
		m.requestLogSettings = requests
		return
	}
	_ = m.requestLogs.Close()
	m.requestLogs = nil
	m.monitor.SetSink(nil)
	m.requestLogSettings = requests
	if !requests.IsEnabled() {
		return
	}
	service, err := logstore.Open(logstore.Options{
		Path: requests.StatePath, RetentionDays: requests.RetentionDays,
		MaxRows: requests.MaxRows, BufferSize: requests.BufferSize, Logger: m.logger,
	})
	if err != nil {
		// A broken log database must not stop the gateway: the in-memory ring
		// still serves the monitor, and the error is reported once per reload.
		m.logger.Error("request log disabled: cannot open database",
			"component", "logstore", "event", "request_log_open_failed", "path", requests.StatePath, "error", err)
		return
	}
	service.Start(m.root)
	m.requestLogs = service
	m.monitor.SetSink(service)
}

func (m *RuntimeManager) ensureEgress(cfg config.Config) error {
	m.egressMu.Lock()
	defer m.egressMu.Unlock()
	if !cfg.EgressEnabled() {
		if m.egressEngine != nil {
			_ = m.egressEngine.Close()
			m.egressEngine = nil
		}
		return nil
	}
	if m.egressEngine == nil {
		engine, err := egress.New(egress.Options{
			Egress:      cfg.Egress,
			Performance: cfg.Performance,
			Upstream:    cfg.Upstream,
			Legacy:      cfg.LegacyContent(),
			Logger:      m.logger,
		})
		if err != nil {
			return fmt.Errorf("initialize egress: %w", err)
		}
		engine.Start(m.root)
		m.egressEngine = engine
		return nil
	}
	m.egressEngine.Apply(cfg.Egress, cfg.Upstream, cfg.LegacyContent())
	return nil
}

type ResourceSnapshot struct {
	Models    modelcatalog.CatalogSnapshot  `json:"models"`
	Keys      []KeyStatus                   `json:"keys"`
	Proxies   []ProxyStatus                 `json:"proxies"`
	Anonymous bool                          `json:"anonymous"`
	Metadata  modelcatalog.MetadataSnapshot `json:"metadata"`
}

type KeyStatus struct {
	ID            string     `json:"id"`
	Tier          string     `json:"tier"`
	Index         int        `json:"index"`
	ProxyIndex    int        `json:"proxy_index"`
	Failures      uint32     `json:"failures"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
}

type ProxyStatus struct {
	Index     int    `json:"index"`
	Address   string `json:"address"`
	Healthy   bool   `json:"healthy"`
	Checking  bool   `json:"checking"`
	ZenKeys   int    `json:"zen_keys"`
	GoKeys    int    `json:"go_keys"`
	Anonymous bool   `json:"anonymous"`
}

func (m *RuntimeManager) Resources() ResourceSnapshot {
	runtime := m.current.Load()
	if runtime == nil {
		return ResourceSnapshot{}
	}
	gateway := runtime.gateway
	_, models := gateway.availableModels()
	result := ResourceSnapshot{Models: models, Anonymous: gateway.cfg.Anonymous}
	result.Metadata = gateway.catalog.MetadataSnapshot()
	result.Keys = append(result.Keys, keyStatuses("zen", gateway.zenNodes)...)
	result.Keys = append(result.Keys, keyStatuses("go", gateway.goNodes)...)
	gateway.zenNodes.bindingsMu.Lock()
	zenBindings := append([]int(nil), gateway.zenNodes.bindingCount...)
	gateway.zenNodes.bindingsMu.Unlock()
	gateway.goNodes.bindingsMu.Lock()
	goBindings := append([]int(nil), gateway.goNodes.bindingCount...)
	gateway.goNodes.bindingsMu.Unlock()
	for _, proxy := range gateway.transports.items {
		status := ProxyStatus{Index: proxy.index, Address: config.RedactURL(proxy.name), Healthy: proxy.healthy.Load(), Checking: proxy.checking.Load(), Anonymous: gateway.cfg.Anonymous}
		if proxy.index < len(zenBindings) {
			status.ZenKeys = zenBindings[proxy.index]
		}
		if proxy.index < len(goBindings) {
			status.GoKeys = goBindings[proxy.index]
		}
		result.Proxies = append(result.Proxies, status)
	}
	return result
}

func (m *RuntimeManager) DebugModels() ([]modelcatalog.RouteDiagnostic, modelcatalog.MetadataSnapshot) {
	runtime := m.current.Load()
	if runtime == nil {
		return nil, modelcatalog.MetadataSnapshot{}
	}
	gateway := runtime.gateway
	models := gateway.catalog.List()
	result := make([]modelcatalog.RouteDiagnostic, 0, len(models))
	for _, model := range models {
		result = append(result, gateway.catalog.Diagnostic(model, "", len(gateway.cfg.ZenKeys) > 0, len(gateway.cfg.GoKeys) > 0, gateway.cfg.Anonymous))
	}
	metadata := gateway.catalog.MetadataSnapshot()
	return result, metadata
}

func (m *RuntimeManager) DebugRoute(model string, requested protocol.Protocol) modelcatalog.RouteDiagnostic {
	runtime := m.current.Load()
	if runtime == nil {
		return modelcatalog.RouteDiagnostic{Model: model, RequestedProtocol: requested, RouteError: "gateway runtime is unavailable"}
	}
	gateway := runtime.gateway
	return gateway.catalog.Diagnostic(model, requested, len(gateway.cfg.ZenKeys) > 0, len(gateway.cfg.GoKeys) > 0, gateway.cfg.Anonymous)
}

// DebugKeyView is the operator-facing view of one configured upstream key. It
// carries only the redacted suffix and the stable fingerprint, never the value.
type DebugKeyView struct {
	ID            string      `json:"id"`
	Tier          config.Tier `json:"tier"`
	Display       string      `json:"display"`
	Index         int         `json:"index"`
	Failures      uint32      `json:"failures"`
	CooldownUntil *time.Time  `json:"cooldown_until,omitempty"`
}

// DebugKeys lists every configured upstream key per tier so the Playground can
// offer an explicit per-key test target.
func (m *RuntimeManager) DebugKeys() map[string][]DebugKeyView {
	result := map[string][]DebugKeyView{"zen": {}, "go": {}}
	runtime := m.current.Load()
	if runtime == nil {
		return result
	}
	appendKeys := func(tier config.Tier, nodes []*upstreamNode) {
		for _, node := range nodes {
			view := DebugKeyView{
				ID: config.Fingerprint(node.key), Tier: tier, Display: config.MaskValue(node.key),
				Index: node.index, Failures: node.failures.Load(),
			}
			if until := node.cooldownUntil.Load(); until > time.Now().UnixNano() {
				value := time.Unix(0, until).UTC()
				view.CooldownUntil = &value
			}
			result[string(tier)] = append(result[string(tier)], view)
		}
	}
	appendKeys(config.TierZen, runtime.gateway.zenNodes.nodes)
	appendKeys(config.TierGo, runtime.gateway.goNodes.nodes)
	return result
}

func (m *RuntimeManager) DebugKey(tier config.Tier, id string) *DebugKeyView {
	for _, view := range m.DebugKeys()[string(tier)] {
		if view.ID == id {
			selected := view
			return &selected
		}
	}
	return nil
}

// DebugRouteForTier reports the route a diagnostic against one tier would take.
func (m *RuntimeManager) DebugRouteForTier(model string, tier config.Tier) (modelcatalog.RouteDiagnostic, error) {
	runtime := m.current.Load()
	if runtime == nil {
		return modelcatalog.RouteDiagnostic{Model: model, RouteError: "gateway runtime is unavailable"}, fmt.Errorf("gateway runtime is unavailable")
	}
	gateway := runtime.gateway
	hasZen, hasGo := len(gateway.cfg.ZenKeys) > 0, len(gateway.cfg.GoKeys) > 0
	diagnostic := gateway.catalog.Diagnostic(model, "", hasZen, hasGo, false)
	route, err := gateway.catalog.RouteForTier(model, tier, hasZen, hasGo)
	if err != nil {
		diagnostic.RouteError = err.Error()
		return diagnostic, err
	}
	diagnostic.RouteError = ""
	diagnostic.Tier, diagnostic.Anonymous = route.Tier, false
	diagnostic.KeyTiers = []config.Tier{tier}
	diagnostic.NativeProtocol, diagnostic.NativeProtocols = route.Protocol, route.Protocols
	return diagnostic, nil
}

func keyStatuses(tier string, pool *nodePool) []KeyStatus {
	result := make([]KeyStatus, 0, len(pool.nodes))
	for _, node := range pool.nodes {
		status := KeyStatus{ID: config.KeyDisplayID(node.key), Tier: tier, Index: node.index, ProxyIndex: int(node.proxyIndex.Load()), Failures: node.failures.Load()}
		if until := node.cooldownUntil.Load(); until > time.Now().UnixNano() {
			value := time.Unix(0, until).UTC()
			status.CooldownUntil = &value
		}
		result = append(result, status)
	}
	return result
}
