import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { api } from "../lib/api";
import type { DebugModelsResponse, EgressCandidate } from "../lib/types";
import { EmptyState, ErrorNotice, JsonBlock, Loading, PageHeader, StatCard } from "../components/Common";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function DiagnosticsPage() {
  const [model, setModel] = useState("");
  const [target, setTarget] = useState("https://opencode.ai/zen");
  const debug = useQuery({ queryKey: ["debug-models"], queryFn: () => api.get<DebugModelsResponse>("/api/debug/models") });
  const diagnose = useQuery({
    queryKey: ["egress", "diagnose", target],
    queryFn: () => api.get<{ target: string; candidates: EgressCandidate[] }>(`/api/egress/diagnose?target=${encodeURIComponent(target)}`),
    enabled: target.startsWith("http"),
  });

  const models = debug.data?.models ?? [];
  const selected = useMemo(() => {
    if (!model) return models[0];
    return models.find((item) => item.model === model) ?? models[0];
  }, [models, model]);

  if (debug.isLoading && !debug.data) {
    return <Loading label="读取模型路由诊断…" />;
  }
  if (debug.isError) {
    return <ErrorNotice message={formatApiErrorMessage(debug.error)} />;
  }

  return (
    <>
      <PageHeader eyebrow="Diagnostics" title="路由诊断" description="模型路由、Key 选择与出口候选评分。" />

      <div className="stats-grid">
        <StatCard label="模型总数" value={models.length} />
        <StatCard label="Zen 可用" value={models.filter((item) => item.available_zen).length} />
        <StatCard label="Go 可用" value={models.filter((item) => item.available_go).length} />
        <StatCard label="路由异常" value={models.filter((item) => item.route_error).length} tone={models.some((item) => item.route_error) ? "danger" : "success"} />
      </div>

      <div className="grid-2">
        <div className="card panel">
          <div className="panel-head">
            <h3>模型路由</h3>
            <Search size={15} className="muted" />
          </div>
          <label className="field-group">
            <span className="field-label">选择模型</span>
            <Select value={selected?.model ?? ""} onChange={(event) => setModel(event.target.value)}>
              {models.map((item) => (
                <option key={item.model} value={item.model}>
                  {item.model}
                </option>
              ))}
            </Select>
          </label>
          {!selected ? (
            <EmptyState title="目录为空" />
          ) : (
            <>
              <div className="timeline">
                <div className="timeline-item">
                  <span className="muted">原生协议</span>
                  <span className="mono">{selected.native_protocol}</span>
                  <span className="badge badge-info">{selected.protocol_source}</span>
                </div>
                <div className="timeline-item">
                  <span className="muted">可用性</span>
                  <span>Zen {selected.available_zen ? "可用" : "不可用"} · Go {selected.available_go ? "可用" : "不可用"}</span>
                  <span className={`badge ${selected.route_error ? "badge-danger" : "badge-success"}`}>
                    {selected.route_error || "正常"}
                  </span>
                </div>
                <div className="timeline-item">
                  <span className="muted">匿名通道</span>
                  <span>{selected.anonymous_eligibility?.allowed ? "允许" : "不允许"}（{selected.anonymous_eligibility?.source}）</span>
                  <span className="badge badge-neutral">{selected.anonymous ? "是" : "否"}</span>
                </div>
              </div>
              <JsonBlock value={selected} />
            </>
          )}
        </div>

        <div className="card panel">
          <div className="panel-head">
            <h3>出口候选</h3>
            <span className="badge badge-neutral">评分目标</span>
          </div>
          <label className="field-group">
            <span className="field-label">上游目标</span>
            <Input value={target} onChange={(event) => setTarget(event.target.value)} placeholder="https://opencode.ai/zen" />
          </label>
          {diagnose.isError ? <ErrorNotice message={formatApiErrorMessage(diagnose.error)} /> : null}
          {!diagnose.data ? (
            <EmptyState title="等待诊断" hint="输入上游 URL 后按目标评估出口" />
          ) : diagnose.data.candidates.length === 0 ? (
            <EmptyState title="出口池未启用或无节点" />
          ) : (
            <div className="data-table-wrap scroll-x">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>节点</th>
                    <th>资格</th>
                    <th>H</th>
                    <th>L</th>
                    <th>R</th>
                    <th>U</th>
                    <th>总分</th>
                  </tr>
                </thead>
                <tbody>
                  {diagnose.data.candidates.slice().sort((a, b) => b.score.total - a.score.total).map((item) => (
                    <tr key={item.id}>
                      <td>
                        {item.name}
                        <div className="muted mono">{item.protocol} · {item.id.slice(0, 8)}</div>
                      </td>
                      <td>
                        <span className={`badge ${item.eligible ? "badge-success" : "badge-muted"}`}>
                          {item.eligible ? "可调度" : item.reason}
                        </span>
                      </td>
                      <td>{formatPercent(item.score.health)}</td>
                      <td>{formatPercent(item.score.latency)}</td>
                      <td>{formatPercent(item.score.routing)}</td>
                      <td>{formatPercent(item.score.upstream)}</td>
                      <td>
                        <strong>{item.score.total.toFixed(1)}</strong>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>上游 Key 诊断</h3>
          <span className="badge badge-neutral">{Object.values(debug.data?.keys ?? {}).flat().length} 个</span>
        </div>
        <div className="grid-2">
          {Object.entries(debug.data?.keys ?? {}).map(([tier, keys]) => (
            <div key={tier}>
              <h4 className="panel-sub">{tier}</h4>
              {keys.length === 0 ? (
                <EmptyState title="未配置" />
              ) : (
                <div className="timeline">
                  {keys.map((key) => (
                    <div className="timeline-item" key={key.id}>
                      <span className="mono">{key.display || key.id.slice(0, 12)}</span>
                      <span className="muted">失败 {key.failures} · 索引 {key.index}</span>
                      <span className={`badge ${key.cooldown_until ? "badge-warning" : "badge-success"}`}>
                        {key.cooldown_until ? "冷却中" : "可用"}
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </>
  );
}
