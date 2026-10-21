import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Pause, Play } from "lucide-react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "../lib/api";
import type { LogEvent, MonitorResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { formatDateTime, formatNumber, formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function MonitorPage() {
  const monitor = useQuery({
    queryKey: ["monitor", "live"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    refetchInterval: 3_000,
  });
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
    return <Loading label="读取实时监控…" />;
  }
  if (monitor.isError) {
    return <ErrorNotice message={formatApiErrorMessage(monitor.error)} />;
  }
  const data = monitor.data;
  if (!data) return <EmptyState />;
  const metrics = data.metrics;

  return (
    <>
      <PageHeader
        eyebrow="Live"
        title="实时监控"
        description="运行指标与实时结构化日志；日志流为管理面 SSE，与推理流互不影响。"
        actions={
          <Button variant="secondary" onClick={() => setPaused((value) => !value)}>
            {paused ? <Play size={15} /> : <Pause size={15} />}
            {paused ? "恢复日志" : "暂停日志"}
          </Button>
        }
      />

      <div className="stats-grid">
        <StatCard
          label="活跃请求（当前）"
          value={formatNumber(metrics.active_requests)}
          hint={`流式 ${metrics.active_streams} 个`}
        />
        <StatCard
          label="累计请求"
          value={formatNumber(metrics.lifetime.total)}
          hint={`成功率 ${formatPercent(metrics.lifetime.success_rate)}`}
        />
        <StatCard
          label="请求数（近 1 小时）"
          value={formatNumber(metrics.last_hour.total)}
          hint={`P95 ${metrics.last_hour.p95_ms || 0} ms`}
        />
        <StatCard
          label="累计 Token"
          value={formatNumber(metrics.usage.lifetime.tokens.total_tokens)}
          hint={`已上报 ${metrics.usage.lifetime.reported} 次`}
        />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <div>
            <h3>分钟级请求与 Token</h3>
            <p className="panel-sub">最近 60 分钟 · 请求单位：次 · Token 单位：个</p>
          </div>
          <span className="badge badge-info">3s 刷新</span>
        </div>
        <div className="chart-wrap">
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
                <Bar dataKey="total_tokens" className="chart-series-warning" fill="var(--warning)" radius={[3, 3, 0, 0]} />
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
                .slice(0, 8)
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
          <span className="badge badge-neutral">
            {logs.length} 条{paused ? " · 已暂停" : ""}
          </span>
        </div>
        <DataTable
          columns={logColumns}
          rows={logs.slice().reverse()}
          rowKey={(event) => String(event.sequence)}
          emptyTitle="暂无日志"
          emptyHint="新日志会实时追加到此处"
          caption="实时结构化日志"
          maxHeight={420}
        />
      </div>
    </>
  );
}
