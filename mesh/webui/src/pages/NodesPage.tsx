import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, RefreshCw, Search } from "lucide-react";
import { api } from "../lib/api";
import type { EgressCandidate, EgressNode, EgressSnapshot } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Switch } from "../components/ui/Switch";
import { formatDateTime, formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function NodesPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("all");
  const [detail, setDetail] = useState<EgressNode | null>(null);

  const snapshot = useQuery({
    queryKey: ["egress", "summary"],
    queryFn: () => api.get<EgressSnapshot>("/api/egress"),
    refetchInterval: 10_000,
  });
  const diagnose = useQuery({
    queryKey: ["egress", "diagnose"],
    queryFn: () =>
      api.get<{ target: string; candidates: EgressCandidate[] }>("/api/egress/diagnose"),
    refetchInterval: 15_000,
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.post(`/api/egress/nodes/${id}/enabled`, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["egress", "summary"] });
      queryClient.invalidateQueries({ queryKey: ["egress", "diagnose"] });
    },
  });
  const probe = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/nodes/${id}/probe`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["egress", "summary"] });
      queryClient.invalidateQueries({ queryKey: ["egress", "diagnose"] });
    },
  });

  const candidates = useMemo(() => {
    const map = new Map<string, EgressCandidate>();
    for (const item of diagnose.data?.candidates ?? []) {
      map.set(item.id, item);
    }
    return map;
  }, [diagnose.data]);

  const nodes = snapshot.data?.nodes ?? [];
  const filtered = nodes.filter((node) => {
    const matchesSearch =
      !search.trim() ||
      node.name.toLowerCase().includes(search.trim().toLowerCase()) ||
      node.id.includes(search.trim()) ||
      (node.server ?? "").includes(search.trim());
    const candidate = candidates.get(node.id);
    const matchesStatus =
      status === "all" ||
      (status === "eligible" && candidate?.eligible) ||
      (status === "cooling" && node.cooldown_until) ||
      (status === "disabled" && !node.enabled) ||
      (status === "unsupported" && !node.supported);
    return matchesSearch && matchesStatus;
  });

  if (snapshot.isLoading && !snapshot.data) {
    return <Loading label="读取节点池…" />;
  }
  if (snapshot.isError) {
    return <ErrorNotice message={formatApiErrorMessage(snapshot.error)} />;
  }
  if (!snapshot.data?.enabled) {
    return (
      <>
        <PageHeader eyebrow="Egress" title="代理节点池" description="统一出口调度。当前未启用。" />
        <EmptyState title="出口池未启用" hint="在代理订阅或配置管理中启用 egress" />
      </>
    );
  }

  return (
    <>
      <PageHeader
        eyebrow="Egress"
        title="代理节点池"
        description={diagnose.data?.target ? `评分目标：${diagnose.data.target}` : "按上游目标计算健康、延迟、负载与成功率。"}
        actions={
          <Button variant="secondary" onClick={() => queryClient.invalidateQueries({ queryKey: ["egress"] })}>
            <RefreshCw size={15} /> 刷新
          </Button>
        }
      />

      <div className="stats-grid">
        <StatCard label="节点总数" value={snapshot.data.counts?.total ?? nodes.length} />
        <StatCard label="可用" value={snapshot.data.counts?.eligible ?? 0} tone={(snapshot.data.counts?.eligible ?? 0) > 0 ? "success" : "danger"} />
        <StatCard label="冷却中" value={snapshot.data.counts?.cooling ?? 0} tone="warning" />
        <StatCard label="停用 / 不支持" value={`${snapshot.data.counts?.disabled ?? 0} / ${snapshot.data.counts?.unsupported ?? 0}`} />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>节点列表</h3>
          <div className="field-inline">
            <Search size={15} className="muted" />
            <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索名称 / ID / 地址" />
            <Select value={status} onChange={(event) => setStatus(event.target.value)}>
              <option value="all">全部状态</option>
              <option value="eligible">可调度</option>
              <option value="cooling">冷却中</option>
              <option value="disabled">已停用</option>
              <option value="unsupported">不支持</option>
            </Select>
          </div>
        </div>

        {filtered.length === 0 ? (
          <EmptyState title="没有匹配的节点" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>节点</th>
                  <th>协议</th>
                  <th>订阅</th>
                  <th>健康</th>
                  <th>延迟</th>
                  <th>综合分</th>
                  <th>冷却至</th>
                  <th>启用</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((node) => {
                  const candidate = candidates.get(node.id);
                  return (
                    <tr key={node.id}>
                      <td>
                        <button className="link-button" onClick={() => setDetail(node)}>
                          {node.name}
                        </button>
                        <div className="muted mono">{node.id.slice(0, 12)}…</div>
                      </td>
                      <td>
                        <span className="tag">{node.protocol}</span>
                        {!node.supported ? <span className="badge badge-muted">unsupported</span> : null}
                      </td>
                      <td className="mono">{(node.subscriptions ?? []).join(", ") || "-"}</td>
                      <td>{formatPercent(node.health_score)}</td>
                      <td>{node.has_latency ? `${Math.round(node.latency_ms ?? 0)} ms` : "-"}</td>
                      <td>
                        {candidate ? (
                          <>
                            <strong>{candidate.score.total.toFixed(1)}</strong>
                            <div className="muted">H {candidate.score.health.toFixed(2)} · L {candidate.score.latency.toFixed(2)}</div>
                          </>
                        ) : (
                          "-"
                        )}
                      </td>
                      <td>{node.cooldown_until ? formatDateTime(node.cooldown_until) : "-"}</td>
                      <td>
                        <Switch
                          checked={node.enabled}
                          onChange={(event) => toggle.mutate({ id: node.id, enabled: event.target.checked })}
                          aria-label={`切换 ${node.name}`}
                        />
                      </td>
                      <td>
                        <div className="table-actions">
                          <Button size="sm" variant="secondary" disabled={probe.isPending} onClick={() => probe.mutate(node.id)}>
                            <Activity size={13} /> 探测
                          </Button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {toggle.isError || probe.isError ? (
          <ErrorNotice message={formatApiErrorMessage(toggle.error ?? probe.error)} />
        ) : null}
      </div>

      {detail ? (
        <div className="modal-overlay" role="dialog" aria-modal="true" onClick={() => setDetail(null)}>
          <div className="modal-card" onClick={(event) => event.stopPropagation()}>
            <div className="panel-head">
              <h3>{detail.name}</h3>
              <Button variant="ghost" size="sm" onClick={() => setDetail(null)}>
                关闭
              </Button>
            </div>
            <pre className="json-block">{JSON.stringify(detail, null, 2)}</pre>
          </div>
        </div>
      ) : null}
    </>
  );
}
