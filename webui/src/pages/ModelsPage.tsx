import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCcw, Search } from "lucide-react";
import { api } from "../lib/api";
import type { Availability, DebugModelsResponse, RankingResult, RankingsResponse, RankingTasksResponse } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

const fallbackTasks = ["agent-coding", "general-text", "vision"];

export function ModelsPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [task, setTask] = useState("agent-coding");
  const models = useQuery({
    queryKey: ["debug-models"],
    queryFn: () => api.get<DebugModelsResponse>("/api/debug/models"),
  });
  const availability = useQuery({
    queryKey: ["availability"],
    queryFn: () => api.get<{ models: Availability[] }>("/api/models/availability"),
  });
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

  const restore = useMutation({
    mutationFn: (model: string) => api.post("/api/models/restore", { model }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["availability"] }),
  });
  const manual = useMutation({
    mutationFn: ({ model, value }: { model: string; value: boolean }) =>
      api.post("/api/models/manual", { model, manual: value }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["availability"] }),
  });

  const availabilityMap = useMemo(() => {
    const map = new Map<string, Availability>();
    for (const item of availability.data?.models ?? []) {
      map.set(item.model, item);
    }
    return map;
  }, [availability.data]);

  if (models.isLoading && !models.data) {
    return <Loading label="读取模型目录…" />;
  }
  if (models.isError) {
    return <ErrorNotice message={formatApiErrorMessage(models.error)} />;
  }
  const all = models.data?.models ?? [];
  // The composite ranking is the model list's order: eligible models first by
  // score, then gated models, then anything the ranking has not scored yet.
  const ranked = rankings.data?.models ?? [];
  const rankOf = new Map<string, { order: number; rank: number; result: RankingResult }>();
  let eligibleRank = 0;
  ranked.forEach((result, order) => {
    if (result.gate) eligibleRank += 1;
    rankOf.set(result.model_id, { order, rank: result.gate ? eligibleRank : 0, result });
  });
  const filtered = all
    .filter((model) => model.model.toLowerCase().includes(search.trim().toLowerCase()))
    .sort((a, b) => {
      const left = rankOf.get(a.model);
      const right = rankOf.get(b.model);
      if (left && right) return left.order - right.order;
      if (left) return -1;
      if (right) return 1;
      return a.model.localeCompare(b.model);
    });
  const taskOptions = tasks.data?.tasks?.map((item) => ({ value: item.task_type, label: item.label })) ?? [];
  const activeTask = rankings.data?.preset;

  return (
    <>
      <PageHeader
        eyebrow="Models"
        title="模型管理"
        description={
          activeTask
            ? `按「${activeTask.label}」综合排名排序：准入条件 × (能力 + 费用 + 可用性 + 适配)；Zen/Go 合并为同一模型。`
            : "模型目录、原生协议、可用性与免费模型状态。"
        }
        actions={
          <div className="field-inline header-filters">
            <Select value={task} onChange={(event) => setTask(event.target.value)} aria-label="排名基准">
              {(taskOptions.length ? taskOptions : fallbackTasks.map((value) => ({ value, label: value }))).map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
            <Search size={15} className="muted" />
            <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索模型 ID" />
          </div>
        }
      />

      <div className="stats-grid">
        <div className="stat-card">
          <span className="stat-label">目录模型</span>
          <p className="stat-value">{models.data?.catalog.total ?? 0}</p>
          <span className="stat-hint">对外暴露 {models.data?.catalog.exposed ?? 0}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">Zen / Go</span>
          <p className="stat-value">{models.data?.catalog.zen ?? 0} / {models.data?.catalog.go ?? 0}</p>
          <span className="stat-hint">来源 {models.data?.catalog.cache_source || "-"}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">免费模型</span>
          <p className="stat-value">{availability.data?.models.length ?? 0}</p>
          <span className="stat-hint">受可用性探测控制</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">元数据</span>
          <p className="stat-value">{models.data?.metadata.models ?? 0}</p>
          <span className="stat-hint">{models.data?.metadata.ready ? "已就绪" : "未就绪"}</span>
        </div>
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>模型列表</h3>
          <span className="badge badge-neutral">{filtered.length} 项</span>
        </div>
        {filtered.length === 0 ? (
          <EmptyState title="没有匹配的模型" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>综合排名</th>
                  <th>模型</th>
                  <th>原生协议</th>
                  <th>Zen</th>
                  <th>Go</th>
                  <th>匿名</th>
                  <th>免费模型状态</th>
                  <th>路由</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((model) => {
                  const status = availabilityMap.get(model.model);
                  const rank = rankOf.get(model.model);
                  return (
                    <tr key={model.model}>
                      <td>
                        {!rank ? (
                          <span className="muted">-</span>
                        ) : rank.result.gate ? (
                          <div>
                            <span className="badge badge-info">#{rank.rank}</span>
                            <div className="muted">{rank.result.final_score.toFixed(1)} 分</div>
                          </div>
                        ) : (
                          <div>
                            <span className="badge badge-muted">未准入</span>
                            <div className="muted">{rank.result.gate_reasons?.join("；") || "-"}</div>
                          </div>
                        )}
                      </td>
                      <td className="mono">{model.model}</td>
                      <td>
                        <span className="tag">{model.native_protocol}</span>
                        {model.native_protocols && Object.entries(model.native_protocols).length > 1 ? (
                          <div className="muted mono">
                            {Object.entries(model.native_protocols)
                              .map(([tier, protocol]) => `${tier}:${protocol}`)
                              .join(" · ")}
                          </div>
                        ) : null}
                      </td>
                      <td>{model.available_zen ? "可用" : "不可用"}</td>
                      <td>{model.available_go ? "可用" : "不可用"}</td>
                      <td>
                        <span className={`badge ${model.anonymous_eligibility?.allowed ? "badge-success" : "badge-muted"}`}>
                          {model.anonymous_eligibility?.allowed ? "允许" : "不允许"}
                        </span>
                      </td>
                      <td>
                        {!status ? (
                          <span className="muted">付费模型</span>
                        ) : (
                          <div>
                            <span className={`badge ${status.disabled ? "badge-danger" : "badge-success"}`}>
                              {status.disabled ? "不可用" : "可用"}
                            </span>
                            <div className="muted">
                              {status.reason || (status.manual ? "手动控制" : "自动探测")}
                              {status.next_check ? ` · 下次 ${formatDateTime(status.next_check)}` : ""}
                            </div>
                          </div>
                        )}
                      </td>
                      <td>
                        {model.route_error ? <span className="badge badge-danger">{model.route_error}</span> : <span className="badge badge-success">正常</span>}
                      </td>
                      <td>
                        {status ? (
                          <div className="table-actions">
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() => restore.mutate(model.model)}
                              disabled={restore.isPending}
                            >
                              <RotateCcw size={13} /> 恢复
                            </Button>
                            <Button
                              size="sm"
                              variant={status.manual ? "ghost" : "secondary"}
                              onClick={() => manual.mutate({ model: model.model, value: !status.manual })}
                              disabled={manual.isPending}
                            >
                              {status.manual ? "转自动" : "转手动"}
                            </Button>
                          </div>
                        ) : (
                          <span className="muted">-</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {rankings.isError ? <ErrorNotice message={formatApiErrorMessage(rankings.error)} /> : null}
        {restore.isError || manual.isError ? (
          <ErrorNotice message={formatApiErrorMessage(restore.error ?? manual.error)} />
        ) : null}
      </div>
    </>
  );
}
