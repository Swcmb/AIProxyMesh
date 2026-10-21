import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, RefreshCw, RotateCcw } from "lucide-react";
import { api } from "../lib/api";
import type { Availability, DebugModelsResponse, RankingResult, RankingsResponse, RankingTasksResponse } from "../lib/types";
import { ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { FilterBar, SearchField } from "../components/ui/FilterBar";
import { Pagination } from "../components/ui/Pagination";
import { useToast } from "../components/ui/Toast";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

const fallbackTasks = ["agent-coding", "general-text", "vision"];

type ModelRow = DebugModelsResponse["models"][number];

export function ModelsPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [search, setSearch] = useState("");
  const [task, setTask] = useState("agent-coding");
  const [page, setPage] = useState(1);
  const pageSize = 11;
  useEffect(() => {
    setPage(1);
  }, [search, task]);
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
    refetchOnWindowFocus: true,
  });

  const refreshAll = () => {
    rankings.refetch();
    models.refetch();
    availability.refetch();
  };
  const refreshing = rankings.isFetching || models.isFetching || availability.isFetching;

  const restore = useMutation({
    mutationFn: (model: string) => api.post("/api/models/restore", { model }),
    onSuccess: (_data, model) => {
      queryClient.invalidateQueries({ queryKey: ["availability"] });
      queryClient.invalidateQueries({ queryKey: ["rankings"] });
      toast.success(`已恢复 ${model} 的自动探测`);
    },
  });
  const manual = useMutation({
    mutationFn: ({ model, value }: { model: string; value: boolean }) =>
      api.post("/api/models/manual", { model, manual: value }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["availability"] });
      queryClient.invalidateQueries({ queryKey: ["rankings"] });
      toast.success(`${variables.model} 已切换为${variables.value ? "手动" : "自动"}控制`);
    },
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
  // 综合排名即模型管理页的默认顺序：准入模型按分数优先，其次未准入，最后未评分。
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
  const noEligible = ranked.length > 0 && ranked.every((result) => !result.gate);
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const paged = filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize);

  const columns: DataColumn<ModelRow>[] = [
    {
      key: "rank",
      header: "优先级 / 排名",
      cell: (model) => {
        const rank = rankOf.get(model.model);
        if (!rank) {
          return <span className="muted">-</span>;
        }
        if (!rank.result.gate) {
          return (
            <span className="muted" title={rank.result.gate_reasons?.join("；") || "未准入"}>
              未准入
            </span>
          );
        }
        return (
          <span className="field-inline">
            <span className="badge badge-info">#{rank.rank}</span>
            <span className="muted">{rank.result.final_score.toFixed(1)} 分</span>
          </span>
        );
      },
    },
    {
      key: "model",
      header: "模型",
      cell: (model) => (
        <div>
          <span className="mono">{model.model}</span>
          <div className="field-inline">
            <span className="tag">{model.native_protocol}</span>
            {model.native_protocols && Object.entries(model.native_protocols).length > 1 ? (
              <span
                className="muted"
                title={Object.entries(model.native_protocols)
                  .map(([tier, protocol]) => `${tier}:${protocol}`)
                  .join(" · ")}
              >
                多协议
              </span>
            ) : null}
          </div>
        </div>
      ),
    },
    {
      key: "channels",
      header: "渠道",
      cell: (model) => (
        <span className="field-inline">
          <span className={`badge ${model.available_zen ? "badge-success" : "badge-muted"}`}>Zen</span>
          <span className={`badge ${model.available_go ? "badge-success" : "badge-muted"}`}>Go</span>
          <span className={`badge ${model.anonymous_eligibility?.allowed ? "badge-success" : "badge-muted"}`}>匿名</span>
        </span>
      ),
    },
    {
      key: "availability",
      header: "免费状态",
      cell: (model) => {
        const status = availabilityMap.get(model.model);
        if (!status) {
          return <span className="muted">付费模型</span>;
        }
        return (
          <span
            className={`badge ${status.disabled ? "badge-danger" : "badge-success"}`}
            title={`${status.reason || (status.manual ? "手动控制" : "自动探测")}${
              status.next_check ? ` · 下次 ${formatDateTime(status.next_check)}` : ""
            }`}
          >
            {status.disabled ? "不可用" : "可用"}
          </span>
        );
      },
    },
    {
      key: "route",
      header: "路由",
      cell: (model) =>
        model.route_error ? (
          <span className="badge badge-danger" title={model.route_error}>
            异常
          </span>
        ) : (
          <span className="badge badge-success">正常</span>
        ),
    },
    {
      key: "actions",
      header: "操作",
      cell: (model) => {
        const status = availabilityMap.get(model.model);
        if (!status) {
          return <span className="muted">-</span>;
        }
        return (
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
        );
      },
    },
  ];

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
          <Button variant="secondary" onClick={refreshAll} disabled={refreshing}>
            <RefreshCw size={15} className={refreshing ? "spin" : undefined} /> 刷新排名
          </Button>
        }
      />

      {noEligible ? (
        <div className="callout callout-warning">
          <AlertTriangle size={16} />
          <span>
            当前排名没有可准入模型：请检查 Zen/Go Key 与模型元数据。列表暂按模型 ID 排序，配置完成后点击「刷新排名」。
          </span>
        </div>
      ) : null}

      <div className="stats-grid">
        <StatCard label="目录模型数" value={models.data?.catalog.total ?? 0} hint={`对外暴露 ${models.data?.catalog.exposed ?? 0} 个`} />
        <StatCard label="Zen / Go 模型" value={`${models.data?.catalog.zen ?? 0} / ${models.data?.catalog.go ?? 0}`} hint={`来源 ${models.data?.catalog.cache_source || "-"}`} />
        <StatCard label="免费模型数" value={availability.data?.models.length ?? 0} hint="受可用性探测控制" />
        <StatCard label="元数据模型数" value={models.data?.metadata.models ?? 0} hint={models.data?.metadata.ready ? "已就绪" : "未就绪"} />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>模型列表</h3>
          <div className="field-inline">
            {rankings.data?.generated_at ? (
              <span className="badge badge-neutral">排名生成于 {formatDateTime(rankings.data.generated_at)}</span>
            ) : null}
            <span className="badge badge-neutral">{filtered.length} 项</span>
          </div>
        </div>
        <FilterBar
          actions={
            <Select value={task} onChange={(event) => setTask(event.target.value)} aria-label="排名基准" style={{ width: 180 }}>
              {(taskOptions.length ? taskOptions : fallbackTasks.map((value) => ({ value, label: value }))).map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
          }
        >
          <SearchField value={search} onChange={setSearch} placeholder="搜索模型 ID" ariaLabel="搜索模型" />
        </FilterBar>
        <DataTable
          columns={columns}
          rows={paged}
          rowKey={(model) => model.model}
          loading={models.isLoading}
          emptyTitle="没有匹配的模型"
          emptyHint="调整搜索关键字后重试"
          caption="模型目录与排名"
          minWidth={1040}
        />
        <Pagination
          page={currentPage}
          pageSize={pageSize}
          total={filtered.length}
          onPageChange={setPage}
          label={`显示 ${filtered.length === 0 ? 0 : (currentPage - 1) * pageSize + 1}-${Math.min(currentPage * pageSize, filtered.length)} / ${filtered.length}`}
        />
        {rankings.isError ? <ErrorNotice message={formatApiErrorMessage(rankings.error)} /> : null}
        {restore.isError || manual.isError ? (
          <ErrorNotice message={formatApiErrorMessage(restore.error ?? manual.error)} />
        ) : null}
      </div>
    </>
  );
}
