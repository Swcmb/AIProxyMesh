import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { DebugModelsResponse, EgressCandidate } from "../lib/types";
import { EmptyState, ErrorNotice, JsonBlock, Loading, PageHeader, StatCard } from "../components/Common";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

interface DiagnoseResponse {
  target: string;
  total: number;
  eligible: number;
  reasons: Record<string, number>;
  limit: number;
  candidates: EgressCandidate[];
}

const reasonLabels: Record<string, string> = {
  disabled: "手动停用",
  unsupported_protocol: "协议不支持",
  protocol_disabled: "协议未启用",
  subscription_disabled: "订阅停用",
  unprobed: "未探测",
  cooling_down: "冷却中",
  already_attempted: "本轮已试过",
};

export function DiagnosticsPage() {
  const [model, setModel] = useState("");
  const [target, setTarget] = useState("https://opencode.ai/zen");
  // Every keystroke would otherwise re-score the whole pool server side.
  const [debouncedTarget, setDebouncedTarget] = useState(target);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedTarget(target), 300);
    return () => clearTimeout(timer);
  }, [target]);
  const debug = useQuery({ queryKey: ["debug-models"], queryFn: () => api.get<DebugModelsResponse>("/api/debug/models") });
  const diagnose = useQuery({
    queryKey: ["egress", "diagnose", debouncedTarget],
    queryFn: () =>
      api.get<DiagnoseResponse>(
        `/api/egress/diagnose?target=${encodeURIComponent(debouncedTarget)}&limit=200`,
      ),
    enabled: debouncedTarget.startsWith("http"),
  });

  const models = debug.data?.models ?? [];
  const selected = useMemo(() => {
    if (!model) return models[0];
    return models.find((item) => item.model === model) ?? models[0];
  }, [models, model]);

  const candidateColumns: DataColumn<EgressCandidate>[] = [
    {
      key: "node",
      header: "节点",
      cell: (item) => (
        <div>
          {item.name}
          <div className="muted mono">
            {item.protocol} · {item.id.slice(0, 8)}
          </div>
        </div>
      ),
    },
    {
      key: "eligible",
      header: "资格",
      cell: (item) => (
        <span className={`badge ${item.eligible ? "badge-success" : "badge-muted"}`}>
          {item.eligible ? "可调度" : item.reason}
        </span>
      ),
    },
    { key: "health", header: "健康 H", align: "right", cell: (item) => formatPercent(item.score.health) },
    { key: "latency", header: "延迟 L", align: "right", cell: (item) => formatPercent(item.score.latency) },
    { key: "routing", header: "路由 R", align: "right", cell: (item) => formatPercent(item.score.routing) },
    { key: "upstream", header: "上游 U", align: "right", cell: (item) => formatPercent(item.score.upstream) },
    {
      key: "total",
      header: "总分",
      align: "right",
      cell: (item) => <strong>{item.score.total.toFixed(1)}</strong>,
    },
  ];

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
        <StatCard label="模型总数" value={models.length} hint="当前目录" />
        <StatCard label="Zen 可用模型数" value={models.filter((item) => item.available_zen).length} hint="当前目录" />
        <StatCard label="Go 可用模型数" value={models.filter((item) => item.available_go).length} hint="当前目录" />
        <StatCard
          label="路由异常模型数"
          value={models.filter((item) => item.route_error).length}
          tone={models.some((item) => item.route_error) ? "danger" : "success"}
          hint="当前目录"
        />
      </div>

      <div className="grid-2">
        <div className="card panel">
          <div className="panel-head">
            <h3>模型路由</h3>
            <span className="badge badge-neutral">单模型</span>
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
                  <span>
                    Zen {selected.available_zen ? "可用" : "不可用"} · Go{" "}
                    {selected.available_go ? "可用" : "不可用"}
                  </span>
                  <span className={`badge ${selected.route_error ? "badge-danger" : "badge-success"}`}>
                    {selected.route_error || "正常"}
                  </span>
                </div>
                <div className="timeline-item">
                  <span className="muted">匿名通道</span>
                  <span>
                    {selected.anonymous_eligibility?.allowed ? "允许" : "不允许"}（
                    {selected.anonymous_eligibility?.source}）
                  </span>
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
            <span className="badge badge-neutral">
              {diagnose.data ? `${diagnose.data.eligible} / ${diagnose.data.total} 可调度` : "评分目标"}
            </span>
          </div>
          <label className="field-group">
            <span className="field-label">上游目标</span>
            <Input
              value={target}
              onChange={(event) => setTarget(event.target.value)}
              placeholder="https://opencode.ai/zen"
            />
          </label>
          {diagnose.isError ? <ErrorNotice message={formatApiErrorMessage(diagnose.error)} /> : null}
          {diagnose.data && Object.keys(diagnose.data.reasons).length > 0 ? (
            <div className="timeline">
              {Object.entries(diagnose.data.reasons)
                .sort((a, b) => b[1] - a[1])
                .map(([reason, count]) => (
                  <div className="timeline-item" key={reason}>
                    <span className="muted">{reasonLabels[reason] ?? reason}</span>
                    <span className="mono">{reason}</span>
                    <span className="badge badge-muted">{count}</span>
                  </div>
                ))}
            </div>
          ) : null}
          {!diagnose.data ? (
            <EmptyState title="等待诊断" hint="输入上游 URL 后按目标评估出口" />
          ) : diagnose.data.total === 0 ? (
            <EmptyState title="出口池未启用或无节点" />
          ) : diagnose.data.candidates.length === 0 ? (
            <EmptyState title="无可用出口" hint="全部节点都被上面的原因挡住" />
          ) : (
            <>
              <p className="muted">
                按总分排序，最多展示 {diagnose.data.limit} 个可调度出口。
              </p>
              <DataTable
                columns={candidateColumns}
                rows={diagnose.data.candidates}
                rowKey={(item) => item.id}
                caption="出口候选评分"
                minWidth={640}
                maxHeight={260}
              />
            </>
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
                      <span className="muted">
                        失败 {key.failures} · 索引 {key.index}
                      </span>
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
