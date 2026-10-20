import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { ConfigView, EgressSnapshot, EgressSubscription } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Switch } from "../components/ui/Switch";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";
import { configUpdateFromView } from "../lib/config-update";

type SubscriptionDraft = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  refresh_seconds: number;
};

const emptyDraft: SubscriptionDraft = { id: "", name: "", url: "", enabled: true, refresh_seconds: 3600 };

export function SubscriptionsPage() {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<SubscriptionDraft>(emptyDraft);
  const config = useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/api/config") });
  const egress = useQuery({
    queryKey: ["egress", "summary"],
    queryFn: () => api.get<EgressSnapshot>("/api/egress"),
    refetchInterval: 15_000,
  });

  const refresh = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/subscriptions/${id}/refresh`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["egress", "summary"] });
      queryClient.invalidateQueries({ queryKey: ["egress", "nodes"] });
    },
  });

  const saveSubscriptions = useMutation({
    mutationFn: (subscriptions: SubscriptionDraft[]) => {
      const cfg = config.data;
      if (!cfg) throw new Error("配置尚未加载");
      const current = cfg.egress;
      const merged = {
        enabled: current?.enabled ?? true,
        import_legacy_proxies: current?.import_legacy_proxies ?? true,
        schedule: current?.schedule ?? {},
        protocols: current?.protocols ?? {},
        cooldown: current?.cooldown ?? {},
        failover: current?.failover ?? {},
        probe: current?.probe ?? {},
        state_path: current?.state_path,
        max_latency_entries: current?.max_latency_entries,
        subscriptions: subscriptions.map((sub) => ({
          id: sub.id.trim(),
          name: sub.name.trim(),
          url: sub.url.trim(),
          enabled: sub.enabled,
          refresh_seconds: sub.refresh_seconds,
        })),
      };
      return api.put("/api/config", configUpdateFromView(cfg, merged));
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["config"] });
      queryClient.invalidateQueries({ queryKey: ["egress", "summary"] });
    },
  });

  if ((config.isLoading && !config.data) || (egress.isLoading && !egress.data)) {
    return <Loading label="读取订阅…" />;
  }
  if (config.isError) {
    return <ErrorNotice message={formatApiErrorMessage(config.error)} />;
  }
  if (egress.isError) {
    return <ErrorNotice message={formatApiErrorMessage(egress.error)} />;
  }
  const cfg = config.data;
  if (!cfg) return <EmptyState />;
  const subscriptions = egress.data?.subscriptions ?? [];
  const configSubscriptions = cfg.egress?.subscriptions ?? [];

  const draftsFromConfig = (): SubscriptionDraft[] =>
    configSubscriptions.map((sub) => ({
      id: sub.id,
      name: sub.name ?? "",
      url: sub.url ?? "",
      enabled: sub.enabled,
      refresh_seconds: sub.refresh_seconds ?? 0,
    }));

  const add = () => {
    if (!draft.id.trim() || !draft.url.trim()) return;
    const next = [...draftsFromConfig(), draft];
    saveSubscriptions.mutate(next, {
      onSuccess: () => setDraft(emptyDraft),
    });
  };

  const toggle = (sub: EgressSubscription, enabled: boolean) => {
    const next = draftsFromConfig().map((item) => (item.id === sub.id ? { ...item, enabled } : item));
    saveSubscriptions.mutate(next);
  };

  const remove = (sub: EgressSubscription) => {
    if (!window.confirm(`删除订阅 ${sub.name || sub.id}？节点会在下一次刷新时移除。`)) return;
    const next = draftsFromConfig().filter((item) => item.id !== sub.id);
    saveSubscriptions.mutate(next);
  };

  return (
    <>
      <PageHeader
        eyebrow="Subscriptions"
        title="代理订阅"
        description="URL 订阅与旧 proxies/proxyfile 统一按节点去重，健康状态跨订阅共享。"
        actions={
          <Button variant="secondary" onClick={() => queryClient.invalidateQueries({ queryKey: ["egress", "summary"] })}>
            <RefreshCw size={15} /> 刷新列表
          </Button>
        }
      />

      <div className="stats-grid">
        <StatCard label="订阅源" value={subscriptions.length} hint={`${subscriptions.filter((sub) => sub.enabled).length} 个启用`} />
        <StatCard label="导入节点" value={subscriptions.reduce((sum, sub) => sum + sub.nodes, 0)} hint="按订阅重复计数" />
        <StatCard label="不支持协议" value={subscriptions.reduce((sum, sub) => sum + sub.unsupported, 0)} hint="可见但不入候选池" />
        <StatCard label="旧代理导入" value={cfg.egress?.import_legacy_proxies ? "开" : "关"} hint="proxies / proxyfile" />
      </div>

      {!egress.data?.enabled ? <ErrorNotice message="出口池未启用：先在此添加订阅并保存，将自动启用 egress。" /> : null}

      <div className="card panel">
        <div className="panel-head">
          <h3>新增订阅</h3>
          <span className="badge badge-neutral">URL 仅显示 origin</span>
        </div>
        <div className="form-grid-tight">
          <label className="field-group">
            <span className="field-label">ID</span>
            <Input value={draft.id} onChange={(event) => setDraft({ ...draft, id: event.target.value })} placeholder="sub-main" />
          </label>
          <label className="field-group">
            <span className="field-label">名称</span>
            <Input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="主订阅" />
          </label>
          <label className="field-group">
            <span className="field-label">URL</span>
            <Input value={draft.url} onChange={(event) => setDraft({ ...draft, url: event.target.value })} placeholder="https://example.com/sub" />
          </label>
          <label className="field-group">
            <span className="field-label">刷新间隔（秒）</span>
            <Input
              type="number"
              min={0}
              value={draft.refresh_seconds}
              onChange={(event) => setDraft({ ...draft, refresh_seconds: Number(event.target.value) || 0 })}
            />
          </label>
          <div className="field-inline" style={{ alignItems: "flex-end" }}>
            <Button onClick={add} disabled={saveSubscriptions.isPending || !draft.id.trim() || !draft.url.trim()}>
              <Plus size={15} /> 添加并保存
            </Button>
          </div>
        </div>
        {saveSubscriptions.isError ? <ErrorNotice message={formatApiErrorMessage(saveSubscriptions.error)} /> : null}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>订阅状态</h3>
          <span className="badge badge-neutral">{subscriptions.length}</span>
        </div>
        {subscriptions.length === 0 ? (
          <EmptyState title="暂无订阅" hint="legacy 源会在启用 egress 后自动出现" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>订阅</th>
                  <th>格式</th>
                  <th>节点</th>
                  <th>最近拉取</th>
                  <th>下次拉取</th>
                  <th>错误</th>
                  <th>启用</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {subscriptions.map((sub) => (
                  <tr key={sub.id}>
                    <td>
                      <strong>{sub.name || sub.id}</strong>
                      <div className="muted mono">{sub.id}{sub.inline ? " · inline" : ""}</div>
                    </td>
                    <td>{sub.format || "-"}</td>
                    <td>
                      {sub.nodes}
                      <div className="muted">支持 {sub.supported} · 不支持 {sub.unsupported}</div>
                    </td>
                    <td>{sub.last_fetch_at ? formatDateTime(sub.last_fetch_at) : "-"}</td>
                    <td>{sub.next_fetch_at ? formatDateTime(sub.next_fetch_at) : "手动"}</td>
                    <td className="pre-wrap">{sub.last_error || "-"}</td>
                    <td>
                      <Switch checked={sub.enabled} onChange={(event) => toggle(sub, event.target.checked)} aria-label={`切换 ${sub.name}`} />
                    </td>
                    <td>
                      <div className="table-actions">
                        <Button size="sm" variant="secondary" disabled={refresh.isPending} onClick={() => refresh.mutate(sub.id)}>
                          <RefreshCw size={13} /> 刷新
                        </Button>
                        <Button size="sm" variant="danger" disabled={saveSubscriptions.isPending} onClick={() => remove(sub)}>
                          <Trash2 size={13} /> 删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {refresh.isError ? <ErrorNotice message={formatApiErrorMessage(refresh.error)} /> : null}
      </div>
    </>
  );
}
