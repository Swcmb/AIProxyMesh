import { useQuery } from "@tanstack/react-query";
import { Gauge } from "lucide-react";
import { api } from "../lib/api";
import type { QuotaResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function QuotasPage() {
  const quotas = useQuery({
    queryKey: ["quotas"],
    queryFn: () => api.get<QuotaResponse>("/api/model-quotas"),
    refetchInterval: 60_000,
  });

  if (quotas.isLoading && !quotas.data) {
    return <Loading label="查询 Go 套餐额度…" />;
  }
  if (quotas.isError) {
    return <ErrorNotice message={formatApiErrorMessage(quotas.error)} />;
  }
  const data = quotas.data;
  if (!data) {
    return <EmptyState />;
  }

  return (
    <>
      <PageHeader
        eyebrow="Quotas"
        title="额度看板"
        description={data.note}
        actions={<span className="badge badge-info">{data.scope}</span>}
      />

      <div className="stats-grid">
        <StatCard label="Go 账号数" value={data.accounts.length} hint={`数据源 ${data.source}`} />
        <StatCard label="缓存刷新间隔" value={`${data.cache_seconds}s`} hint="面板自动刷新间隔 60s" />
        <StatCard label="可用 Go 模型数" value={data.models.length} hint="仅展示可用性，不代表逐模型余额" />
      </div>

      {data.accounts.length === 0 ? (
        <EmptyState title="未配置 Go Key" hint="在系统设置中新增 go_keys 后可查询共享套餐额度" />
      ) : (
        <div className="grid-2">
          {data.accounts.map((account) => (
            <div className="card panel" key={account.id}>
              <div className="panel-head">
                <div>
                  <h3 className="mono">{account.display}</h3>
                  <p className="panel-sub">检查时间 {formatDateTime(account.checked_at)}</p>
                </div>
                <span
                  className={`badge ${
                    account.status === "ok"
                      ? "badge-success"
                      : account.status === "stale"
                        ? "badge-warning"
                        : "badge-danger"
                  }`}
                >
                  {account.status}
                </span>
              </div>
              {account.error ? <ErrorNotice message={account.error} /> : null}
              {account.windows ? (
                <div className="timeline">
                  {Object.entries(account.windows).map(([name, window]) => (
                    <div key={name} className="quota-row">
                      <div className="panel-head">
                        <span className="muted">{quotaName(name)}</span>
                        <strong>{window.remaining_percent.toFixed(1)}% 剩余</strong>
                      </div>
                      <div
                        className="quota-bar"
                        role="progressbar"
                        aria-label={`${quotaName(name)}剩余额度`}
                        aria-valuenow={window.remaining_percent}
                        aria-valuemin={0}
                        aria-valuemax={100}
                      >
                        <span style={{ width: `${Math.max(0, Math.min(100, window.remaining_percent))}%` }} />
                      </div>
                      <p className="muted">
                        重置时间 {formatDateTime(window.resets_at)} · 状态 {window.status}
                      </p>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState title="暂无额度窗口" hint="上游未返回 rolling/weekly/monthly 数据" />
              )}
            </div>
          ))}
        </div>
      )}

      <div className="card panel">
        <div className="panel-head">
          <h3>Go 可用模型</h3>
          <Gauge size={15} className="muted" />
        </div>
        {data.models.length === 0 ? (
          <EmptyState title="暂无可用 Go 模型" />
        ) : (
          <div className="tag-list">
            {data.models.map((model) => (
              <span className="tag" key={model.id} title={model.protocol}>
                {model.id}
              </span>
            ))}
          </div>
        )}
      </div>
    </>
  );
}

function quotaName(name: string): string {
  switch (name) {
    case "rolling":
      return "滚动窗口";
    case "weekly":
      return "每周额度";
    case "monthly":
      return "每月额度";
    default:
      return name;
  }
}
