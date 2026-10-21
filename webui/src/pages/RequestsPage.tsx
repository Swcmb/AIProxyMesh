import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, RefreshCw, Trash2, X } from "lucide-react";
import { api } from "../lib/api";
import type {
  EgressEvent,
  MonitorResponse,
  RequestLogDetail,
  RequestLogResponse,
} from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
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
  const [params, setParams] = useSearchParams();
  const [tab, setTabState] = useState<Tab>(() => readTab(params.get("tab")));
  const filters = useMemo(() => readFilters(params), [params]);
  const [keyword, setKeyword] = useState(filters.keyword);
  const [detailId, setDetailIdState] = useState<string | null>(params.get("request"));
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
  }, [filters.model, filters.channel, filters.tier, filters.exit, filters.status, filters.result, filters.since, filters.until, filters.keyword, filters.limit]);

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
    queryFn: () => api.get<RequestLogDetail>(`/api/logs/requests/detail?request_id=${encodeURIComponent(detailId ?? "")}`),
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
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["request-logs"] });
      queryClient.invalidateQueries({ queryKey: ["request-log-detail"] });
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
                onClick={() => {
                  if (!window.confirm("清空持久化请求日志？该操作不可撤销。")) return;
                  purge.mutate();
                }}
              >
                <Trash2 size={15} /> 清空日志
              </Button>
            ) : null}
          </>
        }
      />

      <div className="tab-row">
        <button className={tab === "requests" ? "tab-active" : ""} onClick={() => setTab("requests")}>
          持久请求{" "}
          {page ? <span className="badge badge-neutral">{stats?.requests ?? 0}</span> : null}
        </button>
        <button className={tab === "live" ? "tab-active" : ""} onClick={() => setTab("live")}>
          实时尝试{" "}
          {monitor.data ? <span className="badge badge-neutral">{monitor.data.upstream.recent.length}</span> : null}
        </button>
        <button className={tab === "events" ? "tab-active" : ""} onClick={() => setTab("events")}>
          出口事件{" "}
          {events.data ? <span className="badge badge-neutral">{(events.data.events ?? []).length}</span> : null}
        </button>
      </div>

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
                <StatCard label="已记录请求" value={stats?.requests ?? 0} hint={`尝试 ${stats?.attempts ?? 0} 条`} />
                <StatCard label="库大小" value={formatBytes(stats?.size_bytes ?? 0)} hint={stats?.path ?? ""} />
                <StatCard
                  label="保留策略"
                  value={`${stats?.retention_days ?? 0} 天`}
                  hint={`上限 ${stats?.max_rows ?? 0} 条`}
                />
                <StatCard
                  label="丢弃记录"
                  value={stats?.dropped ?? 0}
                  hint={stats?.dropped ? "写入缓冲已满" : "写入未阻塞请求"}
                />
              </div>
            ) : null}

            <div className="card panel">
              <div className="panel-head">
                <h3>筛选与分页</h3>
                <span className="badge badge-neutral">筛选条件写入地址栏，可直接分享</span>
              </div>
              <div className="form-grid-tight">
                <label className="field-group">
                  <span className="field-label">关键字</span>
                  <Input value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="请求 / 模型 / 渠道 / 出口" />
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
                  <Input type="datetime-local" value={filters.since} onChange={(event) => patch({ since: event.target.value, before: 0 })} />
                </label>
                <label className="field-group">
                  <span className="field-label">结束时间</span>
                  <Input type="datetime-local" value={filters.until} onChange={(event) => patch({ until: event.target.value, before: 0 })} />
                </label>
                <label className="field-group">
                  <span className="field-label">每页条数</span>
                  <Select value={String(filters.limit)} onChange={(event) => patch({ limit: Number(event.target.value), before: 0 })}>
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
                  {rows.length} 条{stats?.newest ? ` · 最新 ${formatRelativeTime(stats.newest)}` : ""}
                </span>
              </div>
              {rows.length === 0 ? (
                <EmptyState title="暂无匹配的请求" hint="请求完成后约一秒内入库；也可放宽筛选条件" />
              ) : (
                <div className="data-table-wrap scroll-x">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>时间</th>
                        <th>请求</th>
                        <th>模型</th>
                        <th>渠道 / Tier</th>
                        <th>调用方 Key</th>
                        <th>上游 Key</th>
                        <th>出口</th>
                        <th>尝试</th>
                        <th>状态</th>
                        <th>耗时</th>
                        <th>结果</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((row) => (
                        <tr key={row.id}>
                          <td className="nowrap">{formatDateTime(row.time)}</td>
                          <td className="mono">{row.request_id.slice(-8)}</td>
                          <td className="mono">{row.model}</td>
                          <td>
                            {row.channel || "-"}
                            {row.tier ? <div className="muted">{row.tier}</div> : null}
                          </td>
                          <td className="mono">{row.server_key_id ? row.server_key_id.slice(0, 10) : "-"}</td>
                          <td className="mono">{row.key_id || "-"}</td>
                          <td>
                            {row.exit_id ? (
                              <button className="link-button mono" onClick={() => openNode(row.exit_id ?? "")}>
                                {row.exit_node || row.exit_id.slice(0, 8)}
                              </button>
                            ) : (
                              <span className="muted">{row.proxy_node || "-"}</span>
                            )}
                          </td>
                          <td>{row.attempts}</td>
                          <td>{row.status || "-"}</td>
                          <td>{row.duration_ms} ms</td>
                          <td>
                            <span className={`badge ${row.success ? "badge-success" : "badge-danger"}`}>{row.outcome || "-"}</span>
                          </td>
                          <td>
                            <Button size="sm" variant="secondary" onClick={() => setDetailId(row.request_id)}>
                              详情
                            </Button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="pager-row">
                <Button size="sm" variant="secondary" disabled={!history.current.length} onClick={previousPage}>
                  <ChevronLeft size={14} /> 上一页
                </Button>
                <span className="muted">每页 {filters.limit} 条 · 按时间倒序</span>
                <Button size="sm" variant="secondary" disabled={!page?.has_more} onClick={nextPage}>
                  下一页 <ChevronRight size={14} />
                </Button>
              </div>
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

      {tab === "events" ? (
        <div className="card panel">
          <div className="panel-head">
            <h3>出口切换事件</h3>
            <span className="badge badge-neutral">持久化</span>
          </div>
          {events.isError ? <ErrorNotice message={formatApiErrorMessage(events.error)} /> : null}
          {(events.data?.events ?? []).length === 0 ? (
            <EmptyState title="暂无出口事件" hint="请求失败或切换出口后记录" />
          ) : (
            <div className="data-table-wrap scroll-x">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>请求</th>
                    <th>节点</th>
                    <th>目标</th>
                    <th>类型</th>
                    <th>状态</th>
                    <th>尝试</th>
                    <th>原因</th>
                    <th>详情</th>
                  </tr>
                </thead>
                <tbody>
                  {(events.data?.events ?? []).slice().reverse().map((item) => (
                    <tr key={item.id}>
                      <td className="nowrap">{formatDateTime(item.at)}</td>
                      <td className="mono">{item.request_id?.slice(-8) || "-"}</td>
                      <td>
                        {item.node_name || "-"}
                        <div className="muted mono">{item.node_id?.slice(0, 10)}</div>
                      </td>
                      <td className="mono">{item.target || "-"}</td>
                      <td>
                        <span
                          className={`badge ${
                            item.kind === "success" ? "badge-success" : item.kind === "client_cancel" ? "badge-muted" : "badge-warning"
                          }`}
                        >
                          {item.kind}
                        </span>
                      </td>
                      <td>{item.status || "-"}</td>
                      <td>{item.attempt || "-"}</td>
                      <td>{item.reason || "-"}</td>
                      <td className="pre-wrap">{item.detail || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      ) : null}

      {detailId ? (
        <div className="drawer-overlay" onClick={() => setDetailId(null)} role="presentation">
          <div className="card drawer-panel" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-header">
              <div>
                <h3>请求 {detailId.slice(-8)}</h3>
                <p className="mono">{detailId}</p>
              </div>
              <div className="drawer-header-actions">
                <Button size="sm" variant="ghost" onClick={() => setDetailId(null)} aria-label="关闭详情">
                  <X size={14} />
                </Button>
              </div>
            </div>
            {detail.isLoading && !detail.data ? <Loading label="读取请求详情…" /> : null}
            {detail.isError ? <ErrorNotice message={formatApiErrorMessage(detail.error)} /> : null}
            {detail.data ? <RequestDetail data={detail.data} onOpenNode={openNode} /> : null}
          </div>
        </div>
      ) : null}
    </>
  );
}

function RequestDetail({ data, onOpenNode }: { data: RequestLogDetail; onOpenNode: (id: string) => void }) {
  const record = data.request;
  return (
    <div className="platform-drawer-layout">
      <section className="platform-drawer-section">
        <div className="platform-drawer-section-head">
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
      <section className="platform-drawer-section">
        <div className="platform-drawer-section-head">
          <h4>尝试链</h4>
          <p>{data.attempts.length} 条</p>
        </div>
        {data.attempts.length === 0 ? (
          <EmptyState title="该请求没有尝试记录" hint="请求在路由前失败时不会出现尝试" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>时间</th>
                  <th>Tier</th>
                  <th>Key</th>
                  <th>出口</th>
                  <th>状态</th>
                  <th>耗时</th>
                  <th>结果</th>
                </tr>
              </thead>
              <tbody>
                {data.attempts.map((attempt) => (
                  <tr key={attempt.id}>
                    <td>{attempt.attempt}</td>
                    <td className="nowrap">{formatDateTime(attempt.time)}</td>
                    <td>{attempt.tier || "-"}</td>
                    <td className="mono">{attempt.key_id || (attempt.anonymous ? "anonymous" : "-")}</td>
                    <td className="mono">{attempt.exit_node || attempt.proxy_node || "-"}</td>
                    <td>{attempt.status || "-"}</td>
                    <td>{attempt.duration_ms} ms</td>
                    <td>
                      <span className={`badge ${attempt.success ? "badge-success" : "badge-warning"}`}>{attempt.outcome || "-"}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

function LiveAttempts({
  attempts,
  requests,
}: {
  attempts: MonitorResponse["upstream"]["recent"];
  requests: MonitorResponse["upstream"]["requests"];
}) {
  return (
    <>
      <div className="card panel">
        <div className="panel-head">
          <h3>内存环形缓冲中的最近请求</h3>
          <span className="badge badge-neutral">{requests.length}</span>
        </div>
        <p className="panel-sub">进程内的实时视图，重启即清空；持久记录见「持久请求」。</p>
        {requests.length === 0 ? (
          <EmptyState title="暂无请求" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>请求</th>
                  <th>模型</th>
                  <th>渠道</th>
                  <th>出口</th>
                  <th>尝试</th>
                  <th>状态</th>
                  <th>耗时</th>
                  <th>结果</th>
                </tr>
              </thead>
              <tbody>
                {requests.slice().reverse().map((item) => (
                  <tr key={`${item.request_id}-${item.time}`}>
                    <td className="nowrap">{formatDateTime(item.time)}</td>
                    <td className="mono">{item.request_id.slice(-8)}</td>
                    <td className="mono">{item.model}</td>
                    <td>{item.tier || item.channel}</td>
                    <td className="mono">{item.exit_node || item.proxy_node || "-"}</td>
                    <td>{item.attempts}</td>
                    <td>{item.status}</td>
                    <td>{item.duration_ms} ms</td>
                    <td>
                      <span className={`badge ${item.success ? "badge-success" : "badge-danger"}`}>{item.outcome}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>最近上游尝试</h3>
          <span className="badge badge-neutral">{attempts.length}</span>
        </div>
        {attempts.length === 0 ? (
          <EmptyState title="暂无尝试记录" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>模型</th>
                  <th>Tier</th>
                  <th>Key</th>
                  <th>出口</th>
                  <th>#</th>
                  <th>状态</th>
                  <th>耗时</th>
                  <th>结果</th>
                </tr>
              </thead>
              <tbody>
                {attempts.slice(-200).reverse().map((item, index) => (
                  <tr key={`${item.request_id}-${item.attempt}-${index}`}>
                    <td className="nowrap">{formatDateTime(item.time)}</td>
                    <td className="mono">{item.model}</td>
                    <td>{item.tier}</td>
                    <td className="mono">{item.key_id?.slice(0, 10) || "-"}</td>
                    <td className="mono">{item.exit_node || item.proxy_node || "-"}</td>
                    <td>{item.attempt}</td>
                    <td>{item.status || "-"}</td>
                    <td>{item.duration_ms} ms</td>
                    <td>
                      <span className={`badge ${item.success ? "badge-success" : "badge-warning"}`}>{item.outcome}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}
