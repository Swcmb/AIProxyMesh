import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowUpRight, Network, Pause, Play, RefreshCw } from "lucide-react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import type { EgressHealthSummary, EgressSnapshot, LogEvent, MonitorResponse, RotationSnapshot } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard, StatusBadge } from "../components/Common";
import { Button } from "../components/ui/Button";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Tabs } from "../components/ui/Tabs";
import { formatDateTime, formatNumber, formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

type TierRow = { tier: string; total: number };
type View = "overview" | "live";

export function OverviewPage() {
  const monitor = useQuery({
    queryKey: ["monitor", "overview"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    refetchInterval: 3_000,
  });
  const egress = useQuery({
    queryKey: ["egress", "summary"],
    queryFn: () => api.get<EgressSnapshot>("/api/egress"),
    refetchInterval: 10_000,
  });
  const rotation = useQuery({
    queryKey: ["rotation", "overview"],
    queryFn: () => api.get<RotationSnapshot>("/api/rotation"),
    refetchInterval: 15_000,
  });
  const health = useQuery({
    queryKey: ["egress", "health"],
    queryFn: () => api.get<EgressHealthSummary>("/api/egress/health"),
    refetchInterval: 10_000,
  });

  const [view, setView] = useState<View>("overview");
  const [logs, setLogs] = useState<LogEvent[]>([]);
  const [paused, setPaused] = useState(false);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    const source = new EventSource("/api/logs/stream");
    const onLog = (event: MessageEvent) => {
      if (pausedRef.current) return;
      try {
        const data = JSON.parse(event.data) as LogEvent;
        setLogs((current) => [...current.slice(-499), data]);
      } catch {
        // ignore malformed frames
      }
    };
    source.addEventListener("log", onLog as EventListener);
    return () => source.close();
  }, []);

  const series = useMemo(
    () =>
      (monitor.data?.metrics.series ?? []).map((item) => ({
        ...item,
        label: new Date(item.minute).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }),
      })),
    [monitor.data],
  );

  const logColumns: DataColumn<LogEvent>[] = [
    { key: "time", header: "时间", cell: (event) => <span className="nowrap">{formatDateTime(event.time)}</span> },
    {
      key: "level",
      header: "级别",
      cell: (event) => (
        <span
          className={`badge ${
            event.level === "error" ? "badge-danger" : event.level === "warn" ? "badge-warning" : "badge-neutral"
          }`}
        >
          {event.level}
        </span>
      ),
    },
    {
      key: "message",
      header: "消息",
      cell: (event) => (
        <span>
          {event.component ? <span className="tag">{event.component}</span> : null} {event.message}
        </span>
      ),
    },
    {
      key: "fields",
      header: "属性",
      cell: (event) => <span className="mono pre-wrap">{event.fields ? JSON.stringify(event.fields) : "-"}</span>,
    },
  ];

  if (monitor.isLoading && !monitor.data) {
    return <Loading label="读取运行状态…" />;
  }
  if (monitor.isError) {
    return <ErrorNotice message={formatApiErrorMessage(monitor.error)} />;
  }
  const data = monitor.data;
  if (!data) {
    return <EmptyState />;
  }
  const metrics = data.metrics;
  const window = metrics.last_hour;
  const usage = metrics.usage.last_hour.tokens;
  const lifetimeTokens = metrics.usage.lifetime.tokens.total_tokens;
  const counts = egress.data?.counts;
  const tierRows: TierRow[] = Object.entries(metrics.tiers)
    .map(([tier, total]) => ({ tier, total }))
    .sort((a, b) => b.total - a.total);
  const tierColumns: DataColumn<TierRow>[] = [
    { key: "tier", header: "渠道", cell: (row) => row.tier },
    { key: "total", header: "请求数（近 1 小时）", align: "right", cell: (row) => formatNumber(row.total) },
  ];

  const refreshAll = () => {
    monitor.refetch();
    egress.refetch();
    rotation.refetch();
  };

  return (
    <>
      <PageHeader
        eyebrow="Overview · Live"
        title="统一网关总览"
        description={`版本 ${data.version} · 已运行 ${
          metrics.uptime_seconds >= 3600
            ? `${(metrics.uptime_seconds / 3600).toFixed(1)} 小时`
            : `${Math.floor(metrics.uptime_seconds / 60)} 分钟`
        }`}
        actions={
          <Button variant="secondary" onClick={refreshAll} disabled={monitor.isFetching}>
            <RefreshCw size={15} /> 刷新
          </Button>
        }
      />

      <div className="overview-page">
      {health.data?.environment.state === "unavailable" ? (
        <div className="error-notice">
          本地网络或 bootstrap 异常，健康更新已暂停。恢复后将递增环境 epoch 并重新复验。
        </div>
      ) : null}
      {health.data?.environment.state === "degraded" ? (
        <div className="error-notice">
          bootstrap 链路延迟高于预算，节点证据不受影响，但探测已降速。
        </div>
      ) : null}
      <div className="stats-grid">
        <StatCard
          label="请求数（近 1 小时）"
          value={formatNumber(window.total)}
          hint={`成功 ${formatNumber(window.success)} 次`}
        />
        <StatCard
          label="成功率（近 1 小时）"
          value={window.total === 0 ? "-" : formatPercent(window.success_rate)}
          tone={window.total === 0 ? undefined : window.success_rate >= 0.99 ? "success" : window.success_rate >= 0.9 ? "warning" : "danger"}
          hint={window.total === 0 ? "近 1 小时暂无请求" : `错误 ${formatNumber(window.errors)} 次`}
        />
        <StatCard
          label="P95 延迟（近 1 小时）"
          value={window.p95_ms ? `${window.p95_ms} ms` : "-"}
          hint={`平均 ${Math.round(window.average_ms ?? 0)} ms`}
        />
        <StatCard
          label="Token（近 1 小时）"
          value={formatNumber(usage.total_tokens)}
          hint={`输入 ${formatNumber(usage.input_tokens)} · 输出 ${formatNumber(usage.output_tokens)}`}
        />
        <StatCard
          label="活跃请求（当前）"
          value={formatNumber(metrics.active_requests)}
          hint={`流式 ${formatNumber(metrics.active_streams)} 个`}
        />
        <StatCard
          label="出口节点（当前）"
          value={counts ? `${counts.eligible}/${counts.total}` : "-"}
          tone={counts && counts.eligible === 0 ? "danger" : undefined}
          hint={counts ? `冷却 ${counts.cooling} · 停用 ${counts.disabled} · 不支持 ${counts.unsupported}` : "出口池未启用"}
        />
        <StatCard
          label="稳定池覆盖"
          value={health.data ? `${health.data.stable_pool}` : "-"}
          tone={
            health.data && health.data.stable_pool < health.data.stable_min_capacity
              ? "warning"
              : undefined
          }
          hint={
            health.data
              ? `下限 ${health.data.stable_min_capacity} · 验证中 ${health.data.counts.verifying} · 熔断 ${health.data.counts.open}`
              : "健康数据未加载"
          }
        />
        <StatCard
          label="探测预算"
          value={health.data ? `${health.data.probe.budget_per_second}/s` : "-"}
          hint={
            health.data
              ? `今日金丝雀 ${health.data.probe.canary_today}/${health.data.probe.canary_daily_budget}`
              : "探测数据未加载"
          }
        />
      </div>

      <Tabs
        ariaLabel="总览视图"
        value={view}
        onChange={(key) => setView(key as View)}
        items={[
          { key: "overview", label: "运行概览" },
          {
            key: "live",
            label: (
              <>
                实时监控 <span className="badge badge-neutral">{logs.length}</span>
              </>
            ),
          },
        ]}
      />

      {view === "overview" ? (
        <>
          <div className="overview-charts">
            <div className="card panel">
              <div className="panel-head">
                <div>
                  <h3>请求趋势</h3>
                  <p className="panel-sub">最近 60 分钟，按分钟聚合（单位：次）</p>
                </div>
                <span className="badge badge-info">3s 刷新</span>
              </div>
              <div className="chart-wrap chart-wrap-tall">
                {series.length === 0 ? (
                  <EmptyState title="暂无请求" hint="收到第一个网关请求后此处显示趋势" />
                ) : (
                  <ResponsiveContainer width="100%" height="100%">
                    <AreaChart data={series}>
                      <defs>
                        <linearGradient id="overview-total" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" className="chart-stop-primary" stopColor="var(--primary)" stopOpacity={0.3} />
                          <stop offset="100%" className="chart-stop-primary" stopColor="var(--primary)" stopOpacity={0.02} />
                        </linearGradient>
                        <linearGradient id="overview-errors" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" className="chart-stop-danger" stopColor="var(--danger)" stopOpacity={0.3} />
                          <stop offset="100%" className="chart-stop-danger" stopColor="var(--danger)" stopOpacity={0.02} />
                        </linearGradient>
                      </defs>
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                      <XAxis dataKey="label" tickLine={false} axisLine={false} interval="preserveStartEnd" />
                      <YAxis tickLine={false} axisLine={false} allowDecimals={false} width={40} />
                      <Tooltip
                        formatter={(value, name) => [formatNumber(Number(value ?? 0)), String(name) === "total" ? "请求" : "错误"]}
                        labelFormatter={(label) => `时间 ${label}`}
                      />
                      <Area
                        type="monotone"
                        dataKey="total"
                        className="chart-series-primary"
                        stroke="var(--primary)"
                        fill="url(#overview-total)"
                        strokeWidth={2}
                      />
                      <Area
                        type="monotone"
                        dataKey="errors"
                        className="chart-series-danger"
                        stroke="var(--danger)"
                        fill="url(#overview-errors)"
                        strokeWidth={1.5}
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                )}
              </div>
            </div>
          </div>

          <div className="grid-2">
            <div className="card panel">
              <div className="panel-head">
                <h3>出口池健康</h3>
                <Link className="link-inline" to="/nodes">
                  节点池 <Network size={13} />
                </Link>
              </div>
              {!egress.data?.enabled ? (
                <EmptyState title="出口池未启用" hint="在系统设置的 egress 节启用统一出口调度" />
              ) : (
                <div className="timeline">
                  <div className="timeline-item">
                    <span className="muted">可用节点</span>
                    <strong>
                      {egress.data.counts?.eligible ?? 0} / {egress.data.counts?.total ?? 0}
                    </strong>
                    <StatusBadge ok={(egress.data.counts?.eligible ?? 0) > 0} label="可用" pendingLabel="为空" />
                  </div>
                  <div className="timeline-item">
                    <span className="muted">冷却中</span>
                    <strong>{egress.data.counts?.cooling ?? 0}</strong>
                    <span className="badge badge-warning">cooldown</span>
                  </div>
                  <div className="timeline-item">
                    <span className="muted">订阅源</span>
                    <strong>{egress.data.subscriptions?.length ?? 0}</strong>
                    <span className="badge badge-info">subscriptions</span>
                  </div>
                  <div className="timeline-item">
                    <span className="muted">上游成功率（近 1 小时）</span>
                    <strong>{formatPercent(metrics.upstream.last_hour.success_rate)}</strong>
                    <span className="badge badge-neutral">attempts</span>
                  </div>
                </div>
              )}
            </div>

            <div className="card panel">
              <div className="panel-head">
                <h3>渠道与分层</h3>
                <Link className="link-inline" to="/diagnostics">
                  查看诊断 <ArrowUpRight size={13} />
                </Link>
              </div>
              <DataTable
                columns={tierColumns}
                rows={tierRows}
                rowKey={(row) => row.tier}
                loading={monitor.isLoading}
                emptyTitle="暂无渠道数据"
                caption="渠道请求量"
              />
            </div>
          </div>

          <div className="grid-2">
            <div className="card panel">
              <div className="panel-head">
                <h3>当前模型轮转</h3>
                <Link className="link-inline" to="/rotation">
                  管理 <ArrowUpRight size={13} />
                </Link>
              </div>
              {rotation.data?.groups?.length ? (
                <div className="timeline">
                  {rotation.data.groups.map((group) => (
                    <div className="timeline-item" key={group.id}>
                      <span className="muted">{group.alias || group.id}</span>
                      <strong className="mono">{group.current || "-"}</strong>
                      <span className="badge badge-neutral">{group.order?.length ?? 0} 个模型</span>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState title="暂无轮转分组" />
              )}
            </div>
            <div className="card panel">
              <div className="panel-head">
                <h3>运行信息</h3>
                <Activity size={15} className="muted" />
              </div>
              <div className="timeline">
                <div className="timeline-item">
                  <span className="muted">模型目录</span>
                  <strong>
                    {data.resources.models.exposed} / {data.resources.models.total}
                  </strong>
                  <StatusBadge ok={!data.resources.models.stale} label="已更新" pendingLabel="待刷新" />
                </div>
                <div className="timeline-item">
                  <span className="muted">上游 Key</span>
                  <strong>{(data.resources.keys ?? []).length}</strong>
                  <span className="badge badge-info">zen + go</span>
                </div>
                <div className="timeline-item">
                  <span className="muted">匿名通道</span>
                  <strong>{data.resources.anonymous ? "启用" : "关闭"}</strong>
                  <span className="badge badge-neutral">anonymous</span>
                </div>
                <div className="timeline-item">
                  <span className="muted">累计请求</span>
                  <strong>{formatNumber(metrics.lifetime.total)}</strong>
                  <span className="badge badge-neutral">成功率 {formatPercent(metrics.lifetime.success_rate)}</span>
                </div>
                <div className="timeline-item">
                  <span className="muted">累计 Token</span>
                  <strong>{formatNumber(lifetimeTokens)}</strong>
                  <span className="badge badge-neutral">已上报 {metrics.usage.lifetime.reported} 次</span>
                </div>
              </div>
            </div>
          </div>
        </>
      ) : (
        <>
          <div className="card panel">
            <div className="panel-head">
              <div>
                <h3>分钟级请求与 Token</h3>
                <p className="panel-sub">请求单位：次 · Token 单位：个</p>
              </div>
              <span className="badge badge-info">实时</span>
            </div>
            <div className="chart-wrap chart-wrap-tall">
              {series.length === 0 ? (
                <EmptyState title="暂无分钟数据" />
              ) : (
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={series}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickLine={false} axisLine={false} width={42} />
                    <Tooltip
                      formatter={(value, name) => [
                        formatNumber(Number(value ?? 0)),
                        String(name) === "total" ? "请求" : "Token",
                      ]}
                    />
                    <Bar dataKey="total" className="chart-series-primary" fill="var(--primary)" radius={[3, 3, 0, 0]} />
                    <Bar
                      dataKey="total_tokens"
                      className="chart-series-warning"
                      fill="var(--warning)"
                      radius={[3, 3, 0, 0]}
                    />
                  </BarChart>
                </ResponsiveContainer>
              )}
            </div>
          </div>

          <div className="grid-3">
            <div className="card panel">
              <div className="panel-head">
                <h3>状态码分布</h3>
                <span className="badge badge-neutral">累计</span>
              </div>
              {Object.entries(metrics.statuses).length === 0 ? (
                <EmptyState />
              ) : (
                <div className="timeline">
                  {Object.entries(metrics.statuses)
                    .sort((a, b) => Number(a[0]) - Number(b[0]))
                    .map(([status, count]) => (
                      <div className="timeline-item" key={status}>
                        <span className="mono">{status}</span>
                        <strong>{formatNumber(count)}</strong>
                        <span className={`badge ${Number(status) < 400 ? "badge-success" : "badge-danger"}`}>
                          {Number(status) < 400 ? "ok" : "error"}
                        </span>
                      </div>
                    ))}
                </div>
              )}
            </div>
            <div className="card panel">
              <div className="panel-head">
                <h3>模型分布</h3>
                <span className="badge badge-neutral">Top 8</span>
              </div>
              {Object.entries(metrics.models).length === 0 ? (
                <EmptyState />
              ) : (
                <div className="timeline">
                  {Object.entries(metrics.models)
                    .sort((a, b) => b[1] - a[1])
                    .slice(0, 5)
                    .map(([model, count]) => (
                      <div className="timeline-item" key={model}>
                        <span className="mono">{model}</span>
                        <strong>{formatNumber(count)}</strong>
                        <span className="badge badge-neutral">model</span>
                      </div>
                    ))}
                </div>
              )}
            </div>
            <div className="card panel">
              <div className="panel-head">
                <h3>端点分布</h3>
                <span className="badge badge-neutral">累计</span>
              </div>
              {Object.entries(metrics.endpoints).length === 0 ? (
                <EmptyState />
              ) : (
                <div className="timeline">
                  {Object.entries(metrics.endpoints)
                    .sort((a, b) => b[1] - a[1])
                    .slice(0, 5)
                    .map(([endpoint, count]) => (
                      <div className="timeline-item" key={endpoint}>
                        <span className="mono">{endpoint}</span>
                        <strong>{formatNumber(count)}</strong>
                        <span className="badge badge-neutral">endpoint</span>
                      </div>
                    ))}
                </div>
              )}
            </div>
          </div>

          <div className="card panel">
            <div className="panel-head">
              <h3>实时日志</h3>
              <div className="field-inline">
                <span className="badge badge-neutral">
                  {logs.length} 条{paused ? " · 已暂停" : ""}
                </span>
                <Button variant="secondary" size="sm" onClick={() => setPaused((value) => !value)}>
                  {paused ? <Play size={14} /> : <Pause size={14} />}
                  {paused ? "恢复日志" : "暂停日志"}
                </Button>
              </div>
            </div>
            <DataTable
              columns={logColumns}
              rows={logs.slice().reverse()}
              rowKey={(event) => String(event.sequence)}
              emptyTitle="暂无日志"
              emptyHint="新日志会实时追加到此处"
              caption="实时结构化日志"
              maxHeight={190}
            />
          </div>
        </>
      )}
      </div>
    </>
  );
}
