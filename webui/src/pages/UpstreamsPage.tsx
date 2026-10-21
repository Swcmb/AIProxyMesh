import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowUpRight } from "lucide-react";
import { api } from "../lib/api";
import type { ConfigView, MonitorResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatusBadge } from "../components/Common";
import { formatDateTime, formatNumber } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function UpstreamsPage() {
  const config = useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/api/config") });
  const monitor = useQuery({
    queryKey: ["monitor", "upstreams"],
    queryFn: () => api.get<MonitorResponse>("/api/monitor"),
    refetchInterval: 5_000,
  });

  if ((config.isLoading && !config.data) || (monitor.isLoading && !monitor.data)) {
    return <Loading label="读取上游状态…" />;
  }
  if (config.isError) {
    return <ErrorNotice message={formatApiErrorMessage(config.error)} />;
  }
  if (monitor.isError) {
    return <ErrorNotice message={formatApiErrorMessage(monitor.error)} />;
  }
  const cfg = config.data;
  const resources = monitor.data?.resources;
  if (!cfg || !resources) {
    return <EmptyState />;
  }
  const zenKeys = resources.keys.filter((key) => key.tier === "zen");
  const goKeys = resources.keys.filter((key) => key.tier === "go");

  return (
    <>
      <PageHeader
        eyebrow="Upstreams"
        title="上游管理"
        description="Zen / Go Key 池、匿名通道与路由偏好。出口选择由代理节点池负责。"
        actions={
          <Link className="btn btn-primary btn-md" to="/config">
            编辑配置 <ArrowUpRight size={15} />
          </Link>
        }
      />

      <div className="grid-2">
        <div className="card panel">
          <div className="panel-head">
            <h3>上游地址</h3>
            <span className="badge badge-info">{cfg.prefer === "zen" ? "优先 Zen" : "优先 Go"}</span>
          </div>
          <div className="timeline">
            <div className="timeline-item">
              <span className="muted">upstream.zen</span>
              <span className="mono pre-wrap">{cfg.upstream.zen}</span>
              <span className="badge badge-neutral">zen</span>
            </div>
            <div className="timeline-item">
              <span className="muted">upstream.go</span>
              <span className="mono pre-wrap">{cfg.upstream.go}</span>
              <span className="badge badge-neutral">go</span>
            </div>
            <div className="timeline-item">
              <span className="muted">upstream.anonymous</span>
              <span className="mono pre-wrap">{cfg.upstream.anonymous}</span>
              <StatusBadge ok={cfg.anonymous} label="启用" pendingLabel="关闭" />
            </div>
          </div>
          <p className="muted">
            调度器直接从以上地址派生目标与探测目标，不维护第二份目标域名配置。
          </p>
        </div>

        <div className="card panel">
          <div className="panel-head">
            <h3>运行策略</h3>
            <span className="badge badge-neutral">retry</span>
          </div>
          <div className="timeline">
            <div className="timeline-item">
              <span className="muted">最大尝试次数</span>
              <strong>{cfg.retry.max_attempts}</strong>
              <span className="badge badge-info">单预算池</span>
            </div>
            <div className="timeline-item">
              <span className="muted">请求超时</span>
              <strong>{cfg.retry.timeout_seconds}s</strong>
              <span className="badge badge-neutral">timeout</span>
            </div>
            <div className="timeline-item">
              <span className="muted">通道冷却</span>
              <strong>{cfg.performance.failure_cooldown_seconds}s</strong>
              <span className="badge badge-warning">key layer</span>
            </div>
            <div className="timeline-item">
              <span className="muted">尝试超时</span>
              <strong>{cfg.performance.attempt_timeout_seconds || cfg.retry.timeout_seconds}s</strong>
              <span className="badge badge-neutral">attempt</span>
            </div>
          </div>
        </div>
      </div>

      <div className="grid-2">
        <KeyPool title="Zen Key 池" keys={zenKeys} />
        <KeyPool title="Go Key 池" keys={goKeys} />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>旧代理来源</h3>
          <span className="badge badge-neutral">legacy</span>
        </div>
        {(cfg.proxies ?? []).length === 0 && !cfg.proxyfile ? (
          <EmptyState title="未配置 proxies/proxyfile" hint="启用 egress 后默认将直接连接作为出口节点导入" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>地址</th>
                  <th>绑定</th>
                </tr>
              </thead>
              <tbody>
                {resources.proxies.map((proxy) => (
                  <tr key={proxy.index}>
                    <td>{proxy.index}</td>
                    <td className="mono">{proxy.address}</td>
                    <td>
                      <span className={`badge ${proxy.healthy ? "badge-success" : "badge-danger"}`}>
                        {proxy.healthy ? "健康" : "不可用"}
                      </span>
                      <span className="muted"> · zen {proxy.zen_keys} / go {proxy.go_keys}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <p className="muted">
          开启 <span className="mono">egress.import_legacy_proxies</span> 后，proxies/proxyfile 会与订阅一起进入统一节点池并参与去重。
        </p>
      </div>
    </>
  );
}

function KeyPool({
  title,
  keys,
}: {
  title: string;
  keys: { id: string; index: number; failures: number; cooldown_until?: string; proxy_index: number }[];
}) {
  return (
    <div className="card panel">
      <div className="panel-head">
        <h3>{title}</h3>
        <span className="badge badge-neutral">{keys.length} 个</span>
      </div>
      {keys.length === 0 ? (
        <EmptyState title="未配置 Key" hint="在配置管理中新增上游 Key" />
      ) : (
        <div className="data-table-wrap scroll-x">
          <table className="data-table">
            <thead>
              <tr>
                <th>#</th>
                <th>Key</th>
                <th>失败次数</th>
                <th>冷却至</th>
                <th>旧代理绑定</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((key) => (
                <tr key={key.id}>
                  <td>{key.index + 1}</td>
                  <td className="mono">{key.id.slice(0, 10)}</td>
                  <td>{formatNumber(key.failures)}</td>
                  <td>{key.cooldown_until ? formatDateTime(key.cooldown_until) : "-"}</td>
                  <td>{key.proxy_index}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
