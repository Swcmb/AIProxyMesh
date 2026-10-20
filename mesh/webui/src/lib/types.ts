export type Tier = "zen" | "go";
export type Protocol = "chat" | "responses" | "anthropic";

export interface TokenCounts {
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
}

export interface ServerKeyUsage extends TokenCounts {
  requests: number;
}

export interface UsagePeriod {
  requests: number;
  reported: number;
  coverage: number;
  tokens: TokenCounts;
  models: Record<string, TokenCounts>;
  tiers: Record<string, TokenCounts>;
  sessions: Record<string, TokenCounts>;
  server_keys: Record<string, ServerKeyUsage>;
}

export interface UsageSnapshot {
  lifetime: UsagePeriod;
  last_hour: UsagePeriod;
}

export interface MetricSummary {
  total: number;
  success: number;
  errors: number;
  success_rate: number;
  average_ms?: number;
  p50_ms?: number;
  p95_ms?: number;
  p99_ms?: number;
}

export interface MetricSeries {
  minute: string;
  total: number;
  success: number;
  errors: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  total_tokens: number;
  usage_reported: number;
}

export interface UpstreamAttempt {
  time: string;
  request_id: string;
  model: string;
  tier: string;
  attempt: number;
  key_id: string;
  channel: string;
  anonymous: boolean;
  proxy_node: string;
  exit_node?: string;
  exit_id?: string;
  status?: number;
  duration_ms: number;
  success: boolean;
  outcome: string;
}

export interface UpstreamRequest {
  time: string;
  request_id: string;
  session?: string;
  server_key_id?: string;
  model: string;
  tier?: string;
  key_id?: string;
  channel: string;
  anonymous: boolean;
  proxy_node?: string;
  exit_node?: string;
  exit_id?: string;
  attempts: number;
  status: number;
  duration_ms: number;
  success: boolean;
  outcome: string;
}

export interface AttemptCounts {
  total: number;
  success: number;
  failed: number;
}

export interface AttemptAggregate extends AttemptCounts {
  success_rate: number;
  tiers: Record<string, AttemptCounts>;
  channels: Record<string, AttemptCounts>;
  keys: Record<string, AttemptCounts>;
}

export interface UpstreamSnapshot {
  lifetime: AttemptAggregate;
  last_hour: AttemptAggregate;
  requests: UpstreamRequest[];
  recent: UpstreamAttempt[];
}

export interface CatalogSnapshot {
  zen: number;
  go: number;
  total: number;
  exposed: number;
  updated_at?: string;
  cache_source?: string;
  stale: boolean;
}

export interface MetadataSnapshot {
  ready: boolean;
  models: number;
  updated_at?: string;
  stale: boolean;
  last_error?: string;
  next_refresh?: string;
}

export interface KeyStatus {
  id: string;
  tier: Tier;
  index: number;
  proxy_index: number;
  failures: number;
  cooldown_until?: string;
}

export interface ProxyStatus {
  index: number;
  address: string;
  healthy: boolean;
  checking: boolean;
  zen_keys: number;
  go_keys: number;
  anonymous: boolean;
}

export interface ResourceSnapshot {
  models: CatalogSnapshot;
  keys: KeyStatus[];
  proxies: ProxyStatus[];
  anonymous: boolean;
  metadata: MetadataSnapshot;
}

export interface MonitorSnapshot {
  started_at: string;
  uptime_seconds: number;
  active_requests: number;
  active_streams: number;
  lifetime: MetricSummary;
  last_hour: MetricSummary;
  series: MetricSeries[];
  endpoints: Record<string, number>;
  models: Record<string, number>;
  tiers: Record<string, number>;
  statuses: Record<string, number>;
  usage: UsageSnapshot;
  upstream: UpstreamSnapshot;
}

export interface MonitorResponse {
  version: string;
  metrics: MonitorSnapshot;
  usage: UsageSnapshot;
  upstream: UpstreamSnapshot;
  resources: ResourceSnapshot;
}

export interface Availability {
  model: string;
  disabled: boolean;
  checked_at?: string;
  next_check: string;
  reason?: string;
  channel?: string;
  manual?: boolean;
  probe_version?: number;
  auto_effort?: { effort?: string; protocol?: string };
  effort_checked_at?: string;
}

export interface RouteDiagnostic {
  model: string;
  requested_protocol?: string;
  native_protocol: string;
  native_protocols?: Record<string, string>;
  protocol_source: string;
  available_zen: boolean;
  available_go: boolean;
  tier?: Tier;
  anonymous: boolean;
  key_id?: string;
  channel?: string;
  attempts?: number;
  key_tiers?: Tier[];
  anonymous_eligibility?: {
    allowed: boolean;
    source: string;
    known: boolean;
    deprecated: boolean;
    input_cost?: number;
    output_cost?: number;
  };
  route_error?: string;
}

export interface DebugKeyView {
  id: string;
  tier: Tier;
  display: string;
  index: number;
  failures: number;
  cooldown_until?: string;
}

export interface DebugModelsResponse {
  models: RouteDiagnostic[];
  keys: Record<string, DebugKeyView[]>;
  metadata: MetadataSnapshot;
  catalog: CatalogSnapshot;
  last_inference?: DebugInferenceResult;
}

export interface DebugInferenceResult {
  ok: boolean;
  http_status: number;
  duration_ms: number;
  request_id?: string;
  route: RouteDiagnostic;
  selected_key?: DebugKeyView;
  key_test?: string;
  response: unknown;
}

export interface RotationSwitch {
  from?: string;
  to?: string;
  at: string;
  reason?: string;
}

export interface RotationGroup {
  id: string;
  alias: string;
  order: string[];
  current: string;
  generation: number;
  paused: Record<string, string>;
  available: Record<string, boolean>;
  switches: RotationSwitch[];
}

export interface RotationAttempt {
  at: string;
  group: string;
  model: string;
  status: number;
  reason?: string;
}

export interface RotationSnapshot {
  groups: RotationGroup[];
  attempts: RotationAttempt[];
  persistence_error?: string;
  first_output_seconds: number;
}

export interface RankingPreset {
  task_type: string;
  label: string;
  description: string;
  capability_weight: number;
  economy_weight: number;
  availability_weight: number;
  fitness_weight: number;
  needs_tools: boolean;
  needs_vision: boolean;
  min_context_tokens: number;
  context_target: number;
  output_target: number;
}

export interface RankingResult {
  provider: string;
  model_id: string;
  task_type: string;
  gate: boolean;
  gate_reasons?: string[];
  capability_score?: number;
  economy_score?: number;
  availability_score?: number;
  fit_score?: number;
  final_score: number;
  capability_parts?: { l?: number; b?: number; i?: number };
  availability_parts?: { success_rate?: number; latency_score?: number; quota_headroom?: number };
  fit_parts?: { modality?: number; context?: number; tools?: number };
  cost_per_task?: number;
  quota_cost_per_task?: number;
  cost_reference?: number;
  success_rate?: number;
  p95_latency_ms?: number;
  quota_headroom?: number;
  sample_count: number;
  confidence: number;
  updated_at: string;
}

export interface RankingsResponse {
  task_type: string;
  preset: RankingPreset;
  generated_at: string;
  sample_hours: number;
  routes: RankingResult[];
}

export interface RankingTasksResponse {
  tasks: RankingPreset[];
}

export interface EgressCounts {
  total: number;
  eligible: number;
  cooling: number;
  unsupported: number;
  disabled: number;
}

export interface EgressNode {
  id: string;
  name: string;
  protocol: string;
  raw_type?: string;
  server?: string;
  port?: number;
  tls?: boolean;
  enabled: boolean;
  supported: boolean;
  subscriptions: string[];
  failures: number;
  cooldown_until?: string;
  health_score: number;
  latency_ms?: number;
  has_latency: boolean;
  last_error?: string;
  last_probe_at?: string;
  created_at: string;
}

export interface EgressSubscription {
  id: string;
  name: string;
  url?: string;
  enabled: boolean;
  inline?: boolean;
  format?: string;
  nodes: number;
  supported: number;
  unsupported: number;
  added: number;
  removed: number;
  last_error?: string;
  last_fetch_at?: string;
  next_fetch_at?: string;
  refresh_seconds: number;
}

export interface EgressSnapshot {
  enabled: boolean;
  state_path?: string;
  counts?: EgressCounts;
  nodes?: EgressNode[];
  subscriptions?: EgressSubscription[];
}

export interface EgressEvent {
  id: number;
  at: string;
  request_id?: string;
  node_id?: string;
  node_name?: string;
  target?: string;
  kind: string;
  status?: number;
  attempt?: number;
  reason?: string;
  detail?: string;
}

export interface EgressCandidate {
  id: string;
  name: string;
  protocol: string;
  eligible: boolean;
  reason: string;
  score: {
    health: number;
    latency: number;
    routing: number;
    upstream: number;
    total: number;
  };
  cooldown_until?: string;
}

export interface LogEvent {
  sequence: number;
  time: string;
  level: string;
  message: string;
  component?: string;
  fields?: Record<string, unknown>;
}

export interface APIKeyUsage {
  lifetime: ServerKeyUsage;
  last_hour: ServerKeyUsage;
}

export interface APIKeyView {
  id: string;
  name: string;
  display: string;
  enabled: boolean;
  created_at?: string;
  usage: APIKeyUsage;
}

export interface QuotaWindow {
  status: string;
  used_percent: number;
  remaining_percent: number;
  resets_at: string;
}

export interface QuotaAccount {
  id: string;
  display: string;
  status: string;
  error?: string;
  fetched_at?: string;
  checked_at: string;
  windows?: Record<string, QuotaWindow>;
}

export interface QuotaResponse {
  models: { id: string; protocol: string }[];
  accounts: QuotaAccount[];
  scope: string;
  source: string;
  cache_seconds: number;
  catalog: CatalogSnapshot;
  note: string;
}

export interface SecretView {
  id: string;
  display: string;
}

export interface WebUIView {
  enabled: boolean;
  listen: string;
  username: string;
  session_ttl_minutes: number;
}

export interface ConfigView {
  listen: string;
  server_keys: SecretView[];
  zen_keys: SecretView[];
  go_keys: SecretView[];
  anonymous: boolean;
  proxies: SecretView[];
  proxyfile: string;
  upstream: { zen: string; go: string; anonymous: string };
  retry: { max_attempts: number; timeout_seconds: number };
  models: { refresh_seconds: number; protocols: Record<string, string>; free_only: boolean };
  performance: {
    max_idle_conns: number;
    max_idle_conns_per_host: number;
    max_conns_per_host: number;
    idle_conn_timeout_seconds: number;
    connect_timeout_seconds: number;
    failure_cooldown_seconds: number;
    attempt_timeout_seconds: number;
    first_event_timeout_seconds: number;
  };
  logging: { level: string; ring_size: number; dump_request_bodies: boolean };
  prefer: Tier;
  reasoning: { effort?: string; effort_by_model?: Record<string, string> };
  egress?: {
    enabled: boolean;
    subscriptions?: {
      id: string;
      name?: string;
      url?: string;
      enabled: boolean;
      refresh_seconds?: number;
    }[];
    import_legacy_proxies?: boolean;
    schedule: {
      health_weight?: number;
      latency_weight?: number;
      routing_weight?: number;
      upstream_success_weight?: number;
    };
    protocols: { http?: boolean; https?: boolean; socks5?: boolean; socks5h?: boolean };
    cooldown: { base_seconds?: number; max_seconds?: number; recovery_probe?: boolean };
    failover: {
      status_policy?: Record<string, string>;
      request_method_policy?: Record<string, string>;
      honor_retry_after?: boolean;
    };
    probe: {
      targets?: string[];
      interval_seconds?: number;
      timeout_seconds?: number;
      concurrency?: number;
    };
    state_path?: string;
    max_latency_entries?: number;
  };
  webui: WebUIView;
  effective: { listen: string; webui_listen: string; webui_enabled: boolean };
  restart_required_fields?: string[];
}

export interface SessionInfo {
  authenticated: boolean;
  username: string;
  csrf_token: string;
  expires_at: string;
}
