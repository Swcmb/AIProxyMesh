import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowUpRight, Network, RefreshCw } from "lucide-react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import type { EgressSnapshot, MonitorResponse, RotationSnapshot } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard, StatusBadge } from "../components/Common";
import { Button } from "../components/ui/Button";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { formatNumber, formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

type TierRow = { tier: string; total: number };

export function OverviewPage() {
  const monitor = useQuery({
    queryKey: ["monitor", "overview"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    refetchInterval: 5_000,
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
  const counts = egress.data?.counts;
  const series = (metrics.series ?? []).map((item) => ({
    ...item,
    label: new Date(item.minute).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }),
  }));
  const tierRows: TierRow[] = Object.entries(metrics.tiers)
    .map(([tier, total]) => ({ tier, total }))
    .sort((a, b) => b.total - a.total);
  const tierColumns: DataColumn<TierRow>[] = [
    { key: "tier", header: "渠道", cell: (row) => row.tier },
    { key: "total", header: "请求数（近 1 小时）", align: "right", cell: (row) => formatNumber(row.total) },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Overview"
        title="统一网关总览"
        description={`版本 ${data.version} · 已运行 ${metrics.uptime_seconds >= 3600 ? `${(metrics.uptime_seconds / 3600).toFixed(1)} 小时` : `${Math.floor(metrics.uptime_seconds / 60)} 分钟`}`}
        actions={
          <Button variant="secondary" onClick={() => monitor.refetch()}>
            <RefreshCw size={15} /> 刷新
          </Button>
        }
      />

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
      </div>

      <div className="card panel">
        <div className="panel-head">
          <div>
            <h3>请求趋势</h3>
            <p className="panel-sub">最近 60 分钟，按分钟聚合（单位：次）</p>
          </div>
          <span className="badge badge-info">5s 刷新</span>
        </div>
        <div className="chart-wrap">
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

      <div className="grid-2">
        <div className="card panel">
          <div className="panel-head">
            <h3>渠道与分层</h3>
            <Link className="link-inline" to="/monitor">
              查看监控 <ArrowUpRight size={13} />
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
          </div>
        </div>
      </div>
    </>
  );
}
