import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Trophy } from "lucide-react";
import { api } from "../lib/api";
import type { RankingResult, RankingTasksResponse, RankingsResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { formatDateTime, formatPercent } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

const fallbackTasks = ["agent-coding", "general-text", "vision"];

export function RankingPage() {
  const [task, setTask] = useState("agent-coding");
  const tasks = useQuery({
    queryKey: ["ranking-tasks"],
    queryFn: () => api.get<RankingTasksResponse>("/api/rankings/tasks"),
    staleTime: 300_000,
  });
  const rankings = useQuery({
    queryKey: ["rankings", task],
    queryFn: () => api.get<RankingsResponse>(`/api/rankings?task=${encodeURIComponent(task)}`),
    refetchInterval: 60_000,
  });

  if (rankings.isLoading && !rankings.data) {
    return <Loading label="计算模型综合排名…" />;
  }
  if (rankings.isError) {
    return <ErrorNotice message={formatApiErrorMessage(rankings.error)} />;
  }
  const data = rankings.data;
  if (!data) {
    return <EmptyState />;
  }

  const labels = new Map((tasks.data?.tasks ?? []).map((item) => [item.task_type, item.label]));
  const options = (tasks.data?.tasks ?? []).map((item) => item.task_type) || fallbackTasks;
  const routes = data.routes ?? [];
  const eligible = routes.filter((route) => route.gate);
  const best = eligible[0];

  return (
    <>
      <PageHeader
        eyebrow="Rankings"
        title="模型排名"
        description="按「提供商 + 模型 + 任务」计算综合分：准入条件 × (能力 Q + 费用 E + 可用性 A + 适配 F)。"
        actions={<span className="badge badge-info">{labels.get(data.task_type) ?? data.task_type}</span>}
      />

      <div className="button-row">
        {options.map((item) => (
          <button
            key={item}
            type="button"
            className={item === task ? "btn btn-primary" : "btn btn-secondary"}
            onClick={() => setTask(item)}
          >
            {labels.get(item) ?? item}
          </button>
        ))}
      </div>

      <div className="stats-grid">
        <StatCard label="路线总数" value={routes.length} hint={`含 Zen / Go，采样窗口 ${data.sample_hours} 小时`} />
        <StatCard label="通过准入" value={eligible.length} hint={data.preset.needs_tools ? "要求工具调用与最小上下文" : "无硬性能力要求"} />
        <StatCard label="当前最优" value={best ? `${best.provider}/${best.model_id}` : "-"} hint={best ? `综合 ${best.final_score.toFixed(1)} 分` : "暂无满足准入的路线"} />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>{data.preset.description}</h3>
          <span className="badge badge-neutral">
            Q {(data.preset.capability_weight * 100).toFixed(0)}% · E {(data.preset.economy_weight * 100).toFixed(0)}% · A{" "}
            {(data.preset.availability_weight * 100).toFixed(0)}% · F {(data.preset.fitness_weight * 100).toFixed(0)}%
          </span>
        </div>
        {routes.length === 0 ? (
          <EmptyState title="暂无模型路线" hint="等待模型目录完成探测后即可参与排名" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>路线</th>
                  <th>能力 Q</th>
                  <th>费用 E</th>
                  <th>可用 A</th>
                  <th>适配 F</th>
                  <th>综合</th>
                  <th>置信</th>
                  <th>单任务成本</th>
                  <th>P95 / 成功率</th>
                </tr>
              </thead>
              <tbody>
                {routes.map((route, index) => (
                  <RankingRow key={`${route.provider}:${route.model_id}`} route={route} rank={route.gate ? index + 1 : undefined} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>计算说明</h3>
          <Trophy size={15} className="muted" />
        </div>
        <ul className="muted">
          <li>能力 Q 取排行榜、专项基准、内部实测的百分位（50%/30%/20%），缺失项按剩余权重归一化。</li>
          <li>费用 E = 100 / (1 + 单任务成本 / 候选中位成本)；免费路线得 100 分。</li>
          <li>可用性 A = 60% 成功率 + 25% 延迟分 + 15% 额度余量，全部来自运行数据。</li>
          <li>适配 F = 50% 模态 + 25% 上下文 + 25% Agent 能力；硬性要求由准入条件 G 负责。</li>
          <li>样本不足时置信度下降，排名仅作参考，避免新上线路线凭偶然数据冲顶。</li>
        </ul>
        <p className="muted">生成时间 {formatDateTime(data.generated_at)}</p>
      </div>
    </>
  );
}

function RankingRow({ route, rank }: { route: RankingResult; rank?: number }) {
  return (
    <tr>
      <td>{rank ?? "-"}</td>
      <td>
        <span className="tag">{route.provider}</span> <span className="mono">{route.model_id}</span>
        {route.gate ? null : (
          <div className="muted">
            <span className="badge badge-warning">未准入</span> {(route.gate_reasons ?? []).join(" · ")}
          </div>
        )}
        <div className="muted">样本 {route.sample_count}</div>
      </td>
      <td>{scoreCell(route.capability_score, capabilityHint(route))}</td>
      <td>{scoreCell(route.economy_score, route.cost_per_task === undefined ? undefined : `参考中位 ${formatCost(route.cost_reference)}`)}</td>
      <td>{scoreCell(route.availability_score, availabilityHint(route))}</td>
      <td>{scoreCell(route.fit_score, fitHint(route))}</td>
      <td>
        <strong>{route.gate ? route.final_score.toFixed(1) : "-"}</strong>
      </td>
      <td>{formatPercent(route.confidence, 0)}</td>
      <td>{formatCost(route.cost_per_task)}</td>
      <td>
        {route.p95_latency_ms === undefined ? "-" : `${Math.round(route.p95_latency_ms)} ms`}
        <div className="muted">{formatPercent(route.success_rate)}</div>
      </td>
    </tr>
  );
}

function scoreCell(value: number | undefined, hint?: string) {
  if (value === undefined) {
    return <span className="muted">-（无数据）</span>;
  }
  return (
    <>
      {value.toFixed(1)}
      {hint ? <div className="muted">{hint}</div> : null}
    </>
  );
}

function capabilityHint(route: RankingResult): string | undefined {
  const parts = route.capability_parts;
  if (!parts) {
    return undefined;
  }
  return `榜 ${num(parts.l)} · 基准 ${num(parts.b)} · 实测 ${num(parts.i)}`;
}

function availabilityHint(route: RankingResult): string | undefined {
  const parts = route.availability_parts;
  if (!parts) {
    return undefined;
  }
  return `延迟 ${num(parts.latency_score)} · 额度 ${num(parts.quota_headroom)}`;
}

function fitHint(route: RankingResult): string | undefined {
  const parts = route.fit_parts;
  if (!parts) {
    return undefined;
  }
  return `模态 ${num(parts.modality)} · 上下文 ${num(parts.context)} · 工具 ${num(parts.tools)}`;
}

function num(value: number | undefined): string {
  return value === undefined ? "-" : value.toFixed(0);
}

function formatCost(value: number | undefined): string {
  if (value === undefined) {
    return "-";
  }
  if (value === 0) {
    return "免费";
  }
  return `$${value.toFixed(4)}`;
}
