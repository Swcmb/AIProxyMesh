import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { api } from "../lib/api";
import type { EgressEvent, MonitorResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

type Tab = "requests" | "events";

export function RequestsPage() {
  const [tab, setTab] = useState<Tab>("requests");
  const monitor = useQuery({
    queryKey: ["monitor", "requests"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    refetchInterval: 5_000,
  });
  const events = useQuery({
    queryKey: ["egress", "events"],
    queryFn: () => api.get<{ events: EgressEvent[] }>("/api/egress/events?limit=300"),
    refetchInterval: 5_000,
  });

  if (monitor.isLoading && !monitor.data) {
    return <Loading label="读取请求日志…" />;
  }
  if (monitor.isError) {
    return <ErrorNotice message={formatApiErrorMessage(monitor.error)} />;
  }
  const requests = monitor.data?.upstream.requests ?? [];
  const recent = monitor.data?.upstream.recent ?? [];
  const eventItems = events.data?.events ?? [];

  return (
    <>
      <PageHeader
        eyebrow="Observability"
        title="请求与切换日志"
        description="上游请求链与出口切换原因；事件持久化在 egress 状态库。"
        actions={
          <Button
            variant="secondary"
            onClick={() => {
              monitor.refetch();
              events.refetch();
            }}
          >
            <RefreshCw size={15} /> 刷新
          </Button>
        }
      />

      <div className="tab-row">
        <button className={tab === "requests" ? "tab-active" : ""} onClick={() => setTab("requests")}>
          上游请求 <span className="badge badge-neutral">{requests.length}</span>
        </button>
        <button className={tab === "events" ? "tab-active" : ""} onClick={() => setTab("events")}>
          出口事件 <span className="badge badge-neutral">{eventItems.length}</span>
        </button>
      </div>

      {tab === "requests" ? (
        <>
          <div className="card panel">
            <div className="panel-head">
              <h3>最近请求</h3>
              <span className="badge badge-neutral">最多 500 条</span>
            </div>
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
                      <th>Key</th>
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
                        <td className="mono">{item.key_id?.slice(0, 10) || "-"}</td>
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
              <span className="badge badge-neutral">{recent.length}</span>
            </div>
            {recent.length === 0 ? (
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
                    {recent.slice(-200).reverse().map((item, index) => (
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
      ) : (
        <div className="card panel">
          <div className="panel-head">
            <h3>出口切换事件</h3>
            <span className="badge badge-neutral">持久化</span>
          </div>
          {events.isError ? <ErrorNotice message={formatApiErrorMessage(events.error)} /> : null}
          {eventItems.length === 0 ? (
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
                  {eventItems.slice().reverse().map((item) => (
                    <tr key={item.id}>
                      <td className="nowrap">{formatDateTime(item.at)}</td>
                      <td className="mono">{item.request_id?.slice(-8) || "-"}</td>
                      <td>
                        {item.node_name || "-"}
                        <div className="muted mono">{item.node_id?.slice(0, 10)}</div>
                      </td>
                      <td className="mono">{item.target || "-"}</td>
                      <td>
                        <span className={`badge ${item.kind === "success" ? "badge-success" : item.kind === "client_cancel" ? "badge-muted" : "badge-warning"}`}>
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
      )}
    </>
  );
}
