import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Pause, Play } from "lucide-react";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "../lib/api";
import type { LogEvent, MonitorResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
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
        <StatCard label="活跃请求" value={formatNumber(metrics.active_requests)} hint={`流式 ${metrics.active_streams}`} />
        <StatCard label="累计请求" value={formatNumber(metrics.lifetime.total)} hint={`成功率 ${formatPercent(metrics.lifetime.success_rate)}`} />
        <StatCard label="近 1 小时" value={formatNumber(metrics.last_hour.total)} hint={`P95 ${metrics.last_hour.p95_ms || 0} ms`} />
        <StatCard label="累计 Token" value={formatNumber(metrics.usage.lifetime.tokens.total_tokens)} hint={`已上报 ${metrics.usage.lifetime.reported}`} />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>分钟级请求与 Token</h3>
          <span className="badge badge-info">3s 刷新</span>
        </div>
        <div className="chart-wrap">
          {series.length === 0 ? (
            <EmptyState title="暂无分钟数据" />
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={series}>
                <CartesianGrid strokeDasharray="3 3" stroke="rgba(37,72,120,0.12)" />
                <XAxis dataKey="label" tick={{ fontSize: 11 }} interval="preserveStartEnd" />
                <YAxis tick={{ fontSize: 11 }} width={42} />
                <Tooltip formatter={(value, name) => [formatNumber(Number(value ?? 0)), String(name) === "total" ? "请求" : "Token"]} />
                <Bar dataKey="total" fill="#1470ff" radius={[4, 4, 0, 0]} />
                <Bar dataKey="total_tokens" fill="#f0a63a" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>

      <div className="grid-3">
        <div className="card panel">
          <div className="panel-head">
            <h3>状态码</h3>
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
          <span className="badge badge-neutral">{logs.length} 条{paused ? " · 已暂停" : ""}</span>
        </div>
        {logs.length === 0 ? (
          <EmptyState title="暂无日志" hint="新日志会实时追加到此处" />
        ) : (
          <div className="data-table-wrap scroll-x" style={{ maxHeight: 420, overflowY: "auto" }}>
            <table className="data-table">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>级别</th>
                  <th>消息</th>
                  <th>属性</th>
                </tr>
              </thead>
              <tbody>
                {logs.slice().reverse().map((event) => (
                  <tr key={event.sequence}>
                    <td className="nowrap">{formatDateTime(event.time)}</td>
                    <td>
                      <span className={`badge ${event.level === "error" ? "badge-danger" : event.level === "warn" ? "badge-warning" : "badge-neutral"}`}>
                        {event.level}
                      </span>
                    </td>
                    <td>
                      {event.component ? <span className="tag">{event.component}</span> : null} {event.message}
                    </td>
                    <td className="mono pre-wrap">
                      {event.fields ? JSON.stringify(event.fields) : "-"}
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
