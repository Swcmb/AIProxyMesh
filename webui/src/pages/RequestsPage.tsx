import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type {
  EgressEvent,
  MonitorResponse,
  RequestLogAttempt,
  RequestLogDetail,
  RequestLogRecord,
  RequestLogResponse,
} from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import { Pagination } from "../components/ui/Pagination";
import { Select } from "../components/ui/Select";
import { Tabs } from "../components/ui/Tabs";
import { useToast } from "../components/ui/Toast";
import { formatDateTime, formatRelativeTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

type Tab = "requests" | "live" | "events";

const tabs: Tab[] = ["requests", "live", "events"];

function readTab(value: string | null): Tab {
  return tabs.includes(value as Tab) ? (value as Tab) : "requests";
}

const pageSizes = [20, 50, 100, 200, 500];

type LogFilters = {
  keyword: string;
  model: string;
  channel: string;
  tier: string;
  exit: string;
  status: string;
  result: string;
  since: string;
  until: string;
  limit: number;
  before: number;
};

const defaultFilters: LogFilters = {
  keyword: "",
  model: "all",
  channel: "all",
  tier: "all",
  exit: "all",
  status: "",
  result: "all",
  since: "",
  until: "",
  limit: 50,
  before: 0,
};

function readFilters(params: URLSearchParams): LogFilters {
  const limit = Number.parseInt(params.get("limit") ?? "50", 10);
  const before = Number.parseInt(params.get("before") ?? "0", 10);
  return {
    ...defaultFilters,
    keyword: params.get("keyword") ?? "",
    model: params.get("model") ?? "all",
    channel: params.get("channel") ?? "all",
    tier: params.get("tier") ?? "all",
    exit: params.get("exit") ?? "all",
    status: params.get("status") ?? "",
    result: params.get("result") ?? "all",
    since: params.get("since") ?? "",
    until: params.get("until") ?? "",
    limit: pageSizes.includes(limit) ? limit : 50,
    before: Number.isFinite(before) && before > 0 ? before : 0,
  };
}

// toRFC3339 converts a datetime-local value; an empty or unparseable value is
// dropped so a half-filled range never silently filters everything out.
function toRFC3339(value: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
}

function formatBytes(value: number): string {
  if (!value) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function RequestsPage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const [tab, setTabState] = useState<Tab>(() => readTab(params.get("tab")));
  const filters = useMemo(() => readFilters(params), [params]);
  const [keyword, setKeyword] = useState(filters.keyword);
  const [detailId, setDetailIdState] = useState<string | null>(params.get("request"));
  const [purgeOpen, setPurgeOpen] = useState(false);
  // Cursor history is session state: the URL carries the current page only.
  const history = useRef<number[]>([]);

  const setDetailId = useCallback(
    (id: string | null) => {
      setDetailIdState(id);
      const search = new URLSearchParams(window.location.search);
      if (id) search.set("request", id);
      else search.delete("request");
      setParams(search, { replace: true });
    },
    [setParams],
  );

  // The active tab belongs to the URL too, so a link can open straight into
  // 实时尝试 or 出口事件.
  const setTab = useCallback(
    (next: Tab) => {
      setTabState(next);
      const search = new URLSearchParams(window.location.search);
      if (next === "requests") search.delete("tab");
      else search.set("tab", next);
      setParams(search, { replace: true });
    },
    [setParams],
  );

  const patch = useCallback(
    (next: Partial<LogFilters>) => {
      const merged = { ...filters, ...next };
      const search = new URLSearchParams();
      if (merged.keyword) search.set("keyword", merged.keyword);
      if (merged.model !== "all") search.set("model", merged.model);
      if (merged.channel !== "all") search.set("channel", merged.channel);
      if (merged.tier !== "all") search.set("tier", merged.tier);
      if (merged.exit !== "all") search.set("exit", merged.exit);
      if (merged.status) search.set("status", merged.status);
      if (merged.result !== "all") search.set("result", merged.result);
      if (merged.since) search.set("since", merged.since);
      if (merged.until) search.set("until", merged.until);
      if (merged.limit !== defaultFilters.limit) search.set("limit", String(merged.limit));
      if (merged.before > 0) search.set("before", String(merged.before));
      const carried = new URLSearchParams(window.location.search);
      const request = carried.get("request");
      if (request) search.set("request", request);
      const activeTab = carried.get("tab");
      if (activeTab) search.set("tab", activeTab);
      setParams(search, { replace: true });
    },
    [filters, setParams],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (keyword !== filters.keyword) patch({ keyword, before: 0 });
    }, 300);
    return () => window.clearTimeout(timer);
  }, [keyword, filters.keyword, patch]);

  useEffect(() => {
    history.current = [];
  }, [
    filters.model,
    filters.channel,
    filters.tier,
    filters.exit,
    filters.status,
    filters.result,
    filters.since,
    filters.until,
    filters.keyword,
    filters.limit,
  ]);

  const query = useMemo(() => {
    const search = new URLSearchParams();
    if (filters.keyword) search.set("keyword", filters.keyword);
    if (filters.model !== "all") search.set("model", filters.model);
    if (filters.channel !== "all") search.set("channel", filters.channel);
    if (filters.tier !== "all") search.set("tier", filters.tier);
    if (filters.exit !== "all") search.set("exit_id", filters.exit);
    if (filters.status) search.set("status", filters.status);
    if (filters.result === "failed") search.set("outcome", "failed");
    if (filters.result === "success") search.set("success", "true");
    if (filters.since) search.set("since", toRFC3339(filters.since));
    if (filters.until) search.set("until", toRFC3339(filters.until));
    search.set("limit", String(filters.limit));
    if (filters.before > 0) search.set("before", String(filters.before));
    return search.toString();
  }, [filters]);

  const logs = useQuery({
    queryKey: ["request-logs", query],
    queryFn: () => api.get<RequestLogResponse>(`/api/logs/requests?${query}`),
    enabled: tab === "requests",
    refetchInterval: 10_000,
  });
  const detail = useQuery({
    queryKey: ["request-log-detail", detailId],
    queryFn: () =>
      api.get<RequestLogDetail>(`/api/logs/requests/detail?request_id=${encodeURIComponent(detailId ?? "")}`),
    enabled: Boolean(detailId),
  });
  const monitor = useQuery({
    queryKey: ["monitor", "requests"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    enabled: tab !== "requests",
    refetchInterval: 5_000,
  });
  const events = useQuery({
    queryKey: ["egress", "events"],
    queryFn: () => api.get<{ events: EgressEvent[] }>("/api/egress/events?limit=300"),
    enabled: tab === "events",
    refetchInterval: 5_000,
  });

  const purge = useMutation({
    mutationFn: () => api.delete<{ removed: number }>(`/api/logs/requests`),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["request-logs"] });
      queryClient.invalidateQueries({ queryKey: ["request-log-detail"] });
      setPurgeOpen(false);
      toast.success(`已清空 ${result?.removed ?? 0} 条请求日志`);
    },
  });

  const page = logs.data;
  const rows = page?.requests ?? [];
  const facets = page?.facets ?? {};
  const stats = page?.stats;

  const nextPage = () => {
    if (!page?.has_more || !rows.length) return;
    history.current = [...history.current, filters.before];
    patch({ before: page.next_cursor ?? 0 });
  };
  const previousPage = () => {
    if (!history.current.length) return;
    const previous = history.current[history.current.length - 1];
    history.current = history.current.slice(0, -1);
    patch({ before: previous });
  };

  // The node drawer is addressed by query param, so the link deep-links straight
  // into it instead of landing on a bare list.
  const openNode = useCallback((id: string) => navigate(`/nodes?node=${encodeURIComponent(id)}`), [navigate]);

  const requestColumns: DataColumn<RequestLogRecord>[] = [
    { key: "time", header: "时间", cell: (row) => <span className="nowrap">{formatDateTime(row.time)}</span> },
    { key: "request", header: "请求", cell: (row) => <span className="mono">{row.request_id.slice(-8)}</span> },
    { key: "model", header: "模型", cell: (row) => <span className="mono">{row.model}</span> },
    {
      key: "channel",
      header: "渠道 / Tier",
      cell: (row) => (
        <div>
          {row.channel || "-"}
          {row.tier ? <div className="muted">{row.tier}</div> : null}
        </div>
      ),
    },
    {
      key: "server_key",
      header: "调用方 Key",
      cell: (row) => <span className="mono">{row.server_key_id ? row.server_key_id.slice(0, 10) : "-"}</span>,
    },
    { key: "upstream_key", header: "上游 Key", cell: (row) => <span className="mono">{row.key_id || "-"}</span> },
    {
      key: "exit",
      header: "出口",
      cell: (row) =>
        row.exit_id ? (
          <button className="link-button mono" onClick={() => openNode(row.exit_id ?? "")}>
            {row.exit_node || row.exit_id.slice(0, 8)}
          </button>
        ) : (
          <span className="muted">{row.proxy_node || "-"}</span>
        ),
    },
    { key: "attempts", header: "尝试", align: "right", cell: (row) => row.attempts },
    { key: "status", header: "状态", cell: (row) => row.status || "-" },
    { key: "duration", header: "耗时", align: "right", cell: (row) => `${row.duration_ms} ms` },
    {
      key: "outcome",
      header: "结果",
      cell: (row) => (
        <span className={`badge ${row.success ? "badge-success" : "badge-danger"}`}>{row.outcome || "-"}</span>
      ),
    },
    {
      key: "actions",
      header: "操作",
      cell: (row) => (
        <Button size="sm" variant="secondary" onClick={() => setDetailId(row.request_id)}>
          详情
        </Button>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Observability"
        title="请求与切换日志"
        description="持久化的上游请求链、实时尝试与出口切换原因。"
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                if (tab === "requests") logs.refetch();
                else monitor.refetch();
                if (tab === "events") events.refetch();
              }}
            >
              <RefreshCw size={15} /> 刷新
            </Button>
            {tab === "requests" && page?.enabled ? (
              <Button
                variant="danger"
                disabled={purge.isPending || !rows.length}
                onClick={() => setPurgeOpen(true)}
              >
                <Trash2 size={15} /> 清空日志
              </Button>
            ) : null}
          </>
        }
      />

      <Tabs
        ariaLabel="日志视图"
        value={tab}
        onChange={(key) => setTab(key as Tab)}
        items={[
          {
            key: "requests",
            label: (
              <>
                持久请求 {page ? <span className="badge badge-neutral">{stats?.requests ?? 0}</span> : null}
              </>
            ),
          },
          {
            key: "live",
            label: (
              <>
                实时尝试 {monitor.data ? <span className="badge badge-neutral">{monitor.data.upstream.recent.length}</span> : null}
              </>
            ),
          },
          {
            key: "events",
            label: (
              <>
                出口事件 {events.data ? <span className="badge badge-neutral">{(events.data.events ?? []).length}</span> : null}
              </>
            ),
          },
        ]}
      />

      {purge.isError ? <ErrorNotice message={formatApiErrorMessage(purge.error)} /> : null}

      {tab === "requests" ? (
        logs.isLoading && !logs.data ? (
          <Loading label="读取请求日志…" />
        ) : logs.isError ? (
          <ErrorNotice message={formatApiErrorMessage(logs.error)} />
        ) : (
          <>
            {!page?.enabled ? (
              <ErrorNotice message="持久请求日志未启用：在配置中设置 logging.requests.enabled = true 后重启。" />
            ) : null}
            {page?.enabled ? (
              <div className="stats-grid">
                <StatCard label="已记录请求数" value={stats?.requests ?? 0} hint={`尝试 ${stats?.attempts ?? 0} 条`} />
                <StatCard label="数据库大小" value={formatBytes(stats?.size_bytes ?? 0)} hint={stats?.path ?? ""} />
                <StatCard label="保留策略" value={`${stats?.retention_days ?? 0} 天`} hint={`上限 ${stats?.max_rows ?? 0} 条`} />
                <StatCard
                  label="丢弃记录数"
                  value={stats?.dropped ?? 0}
                  hint={stats?.dropped ? "写入缓冲已满" : "写入未阻塞请求"}
                />
              </div>
            ) : null}

            <div className="card panel">
              <div className="panel-head">
                <h3>筛选</h3>
                <span className="badge badge-neutral">筛选条件写入地址栏，可直接分享</span>
              </div>
              <div className="form-grid">
                <label className="field-group">
                  <span className="field-label">关键字</span>
                  <Input
                    value={keyword}
                    onChange={(event) => setKeyword(event.target.value)}
                    placeholder="请求 / 模型 / 渠道 / 出口"
                  />
                </label>
                <label className="field-group">
                  <span className="field-label">模型</span>
                  <Select value={filters.model} onChange={(event) => patch({ model: event.target.value, before: 0 })}>
                    <option value="all">全部模型</option>
                    {(facets.model ?? []).map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">渠道</span>
                  <Select value={filters.channel} onChange={(event) => patch({ channel: event.target.value, before: 0 })}>
                    <option value="all">全部渠道</option>
                    {(facets.channel ?? []).map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">Tier</span>
                  <Select value={filters.tier} onChange={(event) => patch({ tier: event.target.value, before: 0 })}>
                    <option value="all">全部 Tier</option>
                    {(facets.tier ?? []).map((value) => (
                      <option key={value} value={value}>
                        {value}
                      </option>
                    ))}
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">出口节点</span>
                  <Select value={filters.exit} onChange={(event) => patch({ exit: event.target.value, before: 0 })}>
                    <option value="all">全部出口</option>
                    {(facets.exit_id ?? []).map((value) => (
                      <option key={value} value={value}>
                        {value.slice(0, 12)}…
                      </option>
                    ))}
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">结果</span>
                  <Select value={filters.result} onChange={(event) => patch({ result: event.target.value, before: 0 })}>
                    <option value="all">成功与失败</option>
                    <option value="success">仅成功</option>
                    <option value="failed">仅失败</option>
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">HTTP 状态</span>
                  <Input
                    type="number"
                    min={0}
                    max={599}
                    value={filters.status}
                    onChange={(event) => patch({ status: event.target.value.trim(), before: 0 })}
                    placeholder="如 429"
                  />
                </label>
                <label className="field-group">
                  <span className="field-label">起始时间</span>
                  <Input
                    type="datetime-local"
                    value={filters.since}
                    onChange={(event) => patch({ since: event.target.value, before: 0 })}
                  />
                </label>
                <label className="field-group">
                  <span className="field-label">结束时间</span>
                  <Input
                    type="datetime-local"
                    value={filters.until}
                    onChange={(event) => patch({ until: event.target.value, before: 0 })}
                  />
                </label>
                <label className="field-group">
                  <span className="field-label">每页条数</span>
                  <Select
                    value={String(filters.limit)}
                    onChange={(event) => patch({ limit: Number(event.target.value), before: 0 })}
                  >
                    {pageSizes.map((size) => (
                      <option key={size} value={size}>
                        {size}
                      </option>
                    ))}
                  </Select>
                </label>
              </div>
            </div>

            <div className="card panel">
              <div className="panel-head">
                <h3>请求列表</h3>
                <span className="badge badge-neutral">
                  {rows.length} 条
                  {stats?.newest && (stats?.requests ?? 0) > 0 ? ` · 最新 ${formatRelativeTime(stats.newest)}` : ""}
                </span>
              </div>
              <DataTable
                columns={requestColumns}
                rows={rows}
                rowKey={(row) => String(row.id)}
                loading={logs.isLoading}
                emptyTitle="暂无匹配的请求"
                emptyHint="请求完成后约一秒内入库；也可放宽筛选条件"
                caption="持久请求日志"
                minWidth={1120}
              />
              <Pagination
                hasPrev={history.current.length > 0}
                hasNext={Boolean(page?.has_more)}
                onPrev={previousPage}
                onNext={nextPage}
                label={`每页 ${filters.limit} 条 · 按时间倒序`}
              />
            </div>
          </>
        )
      ) : null}

      {tab === "live" ? (
        monitor.isLoading && !monitor.data ? (
          <Loading label="读取实时尝试…" />
        ) : monitor.isError ? (
          <ErrorNotice message={formatApiErrorMessage(monitor.error)} />
        ) : (
          <LiveAttempts attempts={monitor.data?.upstream.recent ?? []} requests={monitor.data?.upstream.requests ?? []} />
        )
      ) : null}

      {tab === "events" ? <EventsPanel events={events.data?.events ?? []} error={events.error} /> : null}

      <Drawer
        open={Boolean(detailId)}
        onClose={() => setDetailId(null)}
        title={detailId ? `请求 ${detailId.slice(-8)}` : "请求详情"}
        subtitle={detailId ?? undefined}
        size="lg"
      >
        {detail.isLoading && !detail.data ? <Loading label="读取请求详情…" /> : null}
        {detail.isError ? <ErrorNotice message={formatApiErrorMessage(detail.error)} /> : null}
        {detail.data ? <RequestDetail data={detail.data} onOpenNode={openNode} /> : null}
      </Drawer>

      <ConfirmDialog
        open={purgeOpen}
        title="清空请求日志"
        description="清空全部持久化请求与尝试记录，该操作不可撤销。"
        confirmLabel="清空"
        danger
        pending={purge.isPending}
        onConfirm={() => purge.mutate()}
        onCancel={() => setPurgeOpen(false)}
      />
    </>
  );
}

function EventsPanel({ events, error }: { events: EgressEvent[]; error: unknown }) {
  const columns: DataColumn<EgressEvent>[] = [
    { key: "time", header: "时间", cell: (item) => <span className="nowrap">{formatDateTime(item.at)}</span> },
    { key: "request", header: "请求", cell: (item) => <span className="mono">{item.request_id?.slice(-8) || "-"}</span> },
    {
      key: "node",
      header: "节点",
      cell: (item) => (
        <div>
          {item.node_name || "-"}
          <div className="muted mono">{item.node_id?.slice(0, 10)}</div>
        </div>
      ),
    },
    { key: "target", header: "目标", cell: (item) => <span className="mono">{item.target || "-"}</span> },
    {
      key: "kind",
      header: "类型",
      cell: (item) => (
        <span
          className={`badge ${
            item.kind === "success" ? "badge-success" : item.kind === "client_cancel" ? "badge-muted" : "badge-warning"
          }`}
        >
          {item.kind}
        </span>
      ),
    },
    { key: "status", header: "状态", cell: (item) => item.status || "-" },
    { key: "attempt", header: "尝试", cell: (item) => item.attempt || "-" },
    { key: "reason", header: "原因", cell: (item) => item.reason || "-" },
    { key: "detail", header: "详情", cell: (item) => <span className="pre-wrap">{item.detail || "-"}</span> },
  ];

  return (
    <div className="card panel">
      <div className="panel-head">
        <h3>出口切换事件</h3>
        <span className="badge badge-neutral">持久化</span>
      </div>
      {error ? <ErrorNotice message={formatApiErrorMessage(error)} /> : null}
      <DataTable
        columns={columns}
        rows={events.slice().reverse()}
        rowKey={(item) => String(item.id)}
        emptyTitle="暂无出口事件"
        emptyHint="请求失败或切换出口后记录"
        caption="出口切换事件"
        minWidth={1000}
        maxHeight={560}
      />
    </div>
  );
}

function RequestDetail({ data, onOpenNode }: { data: RequestLogDetail; onOpenNode: (id: string) => void }) {
  const record = data.request;
  return (
    <div className="detail-stack">
      <section className="detail-section">
        <div className="detail-section-head">
          <h4>请求结果</h4>
          <p>
            <span className={`badge ${record.success ? "badge-success" : "badge-danger"}`}>{record.outcome || "-"}</span>{" "}
            {record.attempts} 次尝试 · {record.status || "-"} · {record.duration_ms} ms
          </p>
        </div>
        <dl className="node-detail-list">
          <div>
            <dt>时间</dt>
            <dd>{formatDateTime(record.time)}</dd>
          </div>
          <div>
            <dt>模型</dt>
            <dd className="mono">{record.model}</dd>
          </div>
          <div>
            <dt>渠道 / Tier</dt>
            <dd>
              {record.channel || "-"} / {record.tier || "-"}
            </dd>
          </div>
          <div>
            <dt>调用方 Key</dt>
            <dd className="mono">{record.server_key_id || "-"}</dd>
          </div>
          <div>
            <dt>上游 Key</dt>
            <dd className="mono">
              {record.key_id || "-"}
              {record.anonymous ? " · 匿名" : ""}
            </dd>
          </div>
          <div>
            <dt>会话</dt>
            <dd className="mono">{record.session || "-"}</dd>
          </div>
          <div>
            <dt>出口</dt>
            <dd className="mono">
              {record.exit_node || record.proxy_node || "direct"}
              {record.exit_id ? (
                <>
                  {" "}
                  <button className="link-button" onClick={() => onOpenNode(record.exit_id ?? "")}>
                    查看节点
                  </button>
                  <div className="muted">{record.exit_id}</div>
                </>
              ) : null}
            </dd>
          </div>
        </dl>
      </section>
      <section className="detail-section">
        <div className="detail-section-head">
          <h4>尝试链</h4>
          <p>{data.attempts.length} 条</p>
        </div>
        {data.attempts.length === 0 ? (
          <EmptyState title="该请求没有尝试记录" hint="请求在路由前失败时不会出现尝试" />
        ) : (
          <AttemptTable attempts={data.attempts} />
        )}
      </section>
    </div>
  );
}

function AttemptTable({ attempts }: { attempts: RequestLogAttempt[] }) {
  const columns: DataColumn<RequestLogAttempt>[] = [
    { key: "attempt", header: "#", cell: (attempt) => attempt.attempt },
    { key: "time", header: "时间", cell: (attempt) => <span className="nowrap">{formatDateTime(attempt.time)}</span> },
    { key: "tier", header: "Tier", cell: (attempt) => attempt.tier || "-" },
    {
      key: "key",
      header: "Key",
      cell: (attempt) => <span className="mono">{attempt.key_id || (attempt.anonymous ? "anonymous" : "-")}</span>,
    },
    {
      key: "exit",
      header: "出口",
      cell: (attempt) => <span className="mono">{attempt.exit_node || attempt.proxy_node || "-"}</span>,
    },
    { key: "status", header: "状态", cell: (attempt) => attempt.status || "-" },
    { key: "duration", header: "耗时", cell: (attempt) => `${attempt.duration_ms} ms` },
    {
      key: "outcome",
      header: "结果",
      cell: (attempt) => (
        <span className={`badge ${attempt.success ? "badge-success" : "badge-warning"}`}>{attempt.outcome || "-"}</span>
      ),
    },
  ];
  return <DataTable columns={columns} rows={attempts} rowKey={(attempt) => String(attempt.id)} caption="尝试链" minWidth={720} />;
}

function LiveAttempts({
  attempts,
  requests,
}: {
  attempts: MonitorResponse["upstream"]["recent"];
  requests: MonitorResponse["upstream"]["requests"];
}) {
  const requestColumns: DataColumn<(typeof requests)[number]>[] = [
    { key: "time", header: "时间", cell: (item) => <span className="nowrap">{formatDateTime(item.time)}</span> },
    { key: "request", header: "请求", cell: (item) => <span className="mono">{item.request_id.slice(-8)}</span> },
    { key: "model", header: "模型", cell: (item) => <span className="mono">{item.model}</span> },
    { key: "channel", header: "渠道", cell: (item) => item.tier || item.channel },
    { key: "exit", header: "出口", cell: (item) => <span className="mono">{item.exit_node || item.proxy_node || "-"}</span> },
    { key: "attempts", header: "尝试", cell: (item) => item.attempts },
    { key: "status", header: "状态", cell: (item) => item.status },
    { key: "duration", header: "耗时", cell: (item) => `${item.duration_ms} ms` },
    {
      key: "outcome",
      header: "结果",
      cell: (item) => <span className={`badge ${item.success ? "badge-success" : "badge-danger"}`}>{item.outcome}</span>,
    },
  ];

  const attemptColumns: DataColumn<(typeof attempts)[number]>[] = [
    { key: "time", header: "时间", cell: (item) => <span className="nowrap">{formatDateTime(item.time)}</span> },
    { key: "model", header: "模型", cell: (item) => <span className="mono">{item.model}</span> },
    { key: "tier", header: "Tier", cell: (item) => item.tier },
    { key: "key", header: "Key", cell: (item) => <span className="mono">{item.key_id?.slice(0, 10) || "-"}</span> },
    { key: "exit", header: "出口", cell: (item) => <span className="mono">{item.exit_node || item.proxy_node || "-"}</span> },
    { key: "attempt", header: "#", cell: (item) => item.attempt },
    { key: "status", header: "状态", cell: (item) => item.status || "-" },
    { key: "duration", header: "耗时", cell: (item) => `${item.duration_ms} ms` },
    {
      key: "outcome",
      header: "结果",
      cell: (item) => <span className={`badge ${item.success ? "badge-success" : "badge-warning"}`}>{item.outcome}</span>,
    },
  ];

  return (
    <>
      <div className="card panel">
        <div className="panel-head">
          <h3>内存环形缓冲中的最近请求</h3>
          <span className="badge badge-neutral">{requests.length}</span>
        </div>
        <p className="panel-sub">进程内的实时视图，重启即清空；持久记录见「持久请求」。</p>
        <DataTable
          columns={requestColumns}
          rows={requests.slice().reverse()}
          rowKey={(item) => `${item.request_id}-${item.time}`}
          emptyTitle="暂无请求"
          caption="实时请求"
          minWidth={960}
          maxHeight={420}
        />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>最近上游尝试</h3>
          <span className="badge badge-neutral">{attempts.length}</span>
        </div>
        <DataTable
          columns={attemptColumns}
          rows={attempts.slice(-200).reverse()}
          rowKey={(item) => `${item.request_id}-${item.attempt}-${item.time}`}
          emptyTitle="暂无尝试记录"
          caption="最近上游尝试"
          minWidth={960}
          maxHeight={420}
        />
      </div>
    </>
  );
}
