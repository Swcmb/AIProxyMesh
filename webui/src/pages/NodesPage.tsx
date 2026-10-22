import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, X, Zap } from "lucide-react";
import { api } from "../lib/api";
import type {
  BatchProbeJob,
  BatchProbeResponse,
  ConfigView,
  EgressNode,
  EgressNodePage,
  GeoIPLookupResponse,
  GeoIPStatusResponse,
} from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Drawer } from "../components/ui/Drawer";
import { FilterBar, SearchField } from "../components/ui/FilterBar";
import { Input } from "../components/ui/Input";
import { Pagination } from "../components/ui/Pagination";
import { Select } from "../components/ui/Select";
import { Switch } from "../components/ui/Switch";
import { useToast } from "../components/ui/Toast";
import { formatDateTime, formatPercent, formatRelativeTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";
import { configUpdateFromView } from "../lib/config-update";
const statusLabels: Record<string, string> = {
  healthy: "健康",
  cooling: "冷却",
  error: "最近失败",
  unprobed: "待测",
  disabled: "停用",
  unsupported: "不支持",
};

const statusTone: Record<string, string> = {
  healthy: "badge-success",
  cooling: "badge-warning",
  error: "badge-danger",
  unprobed: "badge-neutral",
  disabled: "badge-muted",
  unsupported: "badge-muted",
};

const healthLabels: Record<string, string> = {
  candidate: "候选",
  verifying: "验证中",
  active: "稳定",
  suspect: "可疑",
  open: "熔断",
  half_open: "半开",
  dead: "淘汰",
};

const healthTone: Record<string, string> = {
  active: "badge-success",
  suspect: "badge-warning",
  open: "badge-danger",
  half_open: "badge-info",
  dead: "badge-muted",
  verifying: "badge-info",
  candidate: "badge-neutral",
};

const pageSizes = [20, 50, 100, 200, 500];

type Filters = {
  keyword: string;
  status: string;
  subscription: string;
  protocol: string;
  region: string;
  sort_by: string;
  sort_order: "asc" | "desc";
  offset: number;
  limit: number;
};

const defaultFilters: Filters = {
  keyword: "",
  status: "all",
  subscription: "all",
  protocol: "all",
  region: "all",
  sort_by: "score",
  sort_order: "desc",
  offset: 0,
  limit: 50,
};

function readFilters(params: URLSearchParams): Filters {
  const offset = Number.parseInt(params.get("offset") ?? "0", 10);
  const limit = Number.parseInt(params.get("limit") ?? "50", 10);
  return {
    ...defaultFilters,
    keyword: params.get("keyword") ?? "",
    status: params.get("status") ?? "all",
    subscription: params.get("subscription") ?? "all",
    protocol: params.get("protocol") ?? "all",
    region: params.get("region") ?? "all",
    sort_by: params.get("sort_by") ?? "score",
    sort_order: params.get("sort_order") === "asc" ? "asc" : "desc",
    offset: Number.isFinite(offset) && offset > 0 ? offset : 0,
    limit: pageSizes.includes(limit) ? limit : 50,
  };
}

// flagEmoji turns an ISO 3166-1 alpha-2 code into its regional-indicator pair.
function flagEmoji(code: string): string {
  if (code.length !== 2) return "";
  const base = code.toUpperCase().charCodeAt(0) - 65 + 0x1f1e6;
  const second = code.toUpperCase().charCodeAt(1) - 65 + 0x1f1e6;
  return String.fromCodePoint(base, second);
}

export function NodesPage() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => readFilters(params), [params]);
  const [keyword, setKeyword] = useState(filters.keyword);
  const [detailId, setDetailIdState] = useState<string | null>(params.get("node"));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [probeJob, setProbeJob] = useState<BatchProbeJob | null>(null);
  // Tracks jobs this mount actually observed running, so re-entering the page
  // does not replay a completion toast for an old job.
  const observedRunning = useRef<string | null>(null);

  // The open drawer is part of the URL so a node view can be shared directly.
  const setDetailId = useCallback(
    (id: string | null) => {
      setDetailIdState(id);
      const search = new URLSearchParams(window.location.search);
      if (id) search.set("node", id);
      else search.delete("node");
      setParams(search, { replace: true });
    },
    [setParams],
  );

  const patch = useCallback(
    (next: Partial<Filters>) => {
      const merged = { ...filters, ...next };
      const search = new URLSearchParams();
      if (merged.keyword) search.set("keyword", merged.keyword);
      if (merged.status !== "all") search.set("status", merged.status);
      if (merged.subscription !== "all") search.set("subscription", merged.subscription);
      if (merged.protocol !== "all") search.set("protocol", merged.protocol);
      if (merged.region !== "all") search.set("region", merged.region);
      if (merged.sort_by !== defaultFilters.sort_by) search.set("sort_by", merged.sort_by);
      if (merged.sort_order !== defaultFilters.sort_order) search.set("sort_order", merged.sort_order);
      if (merged.offset > 0) search.set("offset", String(merged.offset));
      if (merged.limit !== defaultFilters.limit) search.set("limit", String(merged.limit));
      const node = new URLSearchParams(window.location.search).get("node");
      if (node) search.set("node", node);
      setParams(search, { replace: true });
    },
    [filters, setParams],
  );

  useEffect(() => {
    if (keyword === filters.keyword) return;
    const timer = setTimeout(() => patch({ keyword, offset: 0 }), 300);
    return () => clearTimeout(timer);
  }, [keyword, filters.keyword, patch]);

  // A new filter represents a new working set; stale cross-page selections
  // would otherwise refresh nodes the operator can no longer see.
  const filterKey = queryString(filters);
  useEffect(() => {
    setSelected(new Set());
  }, [filterKey]);

  const query = useQuery({
    queryKey: ["egress", "nodes", filters],
    queryFn: () => api.get<EgressNodePage>(`/api/egress/nodes?${queryString(filters)}`),
    refetchInterval: 15_000,
  });
  const detail = useQuery({
    queryKey: ["egress", "node", detailId],
    queryFn: () => api.get<{ node: EgressNode }>(`/api/egress/nodes/${detailId}`),
    enabled: Boolean(detailId),
    refetchInterval: detailId ? 5_000 : false,
  });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.post(`/api/egress/nodes/${id}/enabled`, { enabled }),
    onSuccess: (_data, variables) => {
      invalidate(queryClient);
      toast.success(`节点 ${variables.id.slice(0, 12)} 已${variables.enabled ? "启用" : "停用"}`);
    },
  });
  const probe = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/nodes/${id}/probe`),
    onSuccess: (_result, id) => {
      invalidate(queryClient);
      queryClient.invalidateQueries({ queryKey: ["egress", "node", id] });
      toast.success("探测请求已完成");
    },
  });
  const batchProbe = useMutation({
    mutationFn: (ids: string[]) => api.post<BatchProbeResponse>("/api/egress/nodes/probe", { ids }),
    onSuccess: (result) => {
      invalidate(queryClient);
      if (result.mode === "sync") {
        toast.success(`已刷新 ${result.probed} 个节点`);
        return;
      }
      setProbeJob(result.job);
      toast.success(result.started ? "批量刷新已开始" : "已有批量刷新在进行，已接入进度");
    },
  });
  const refreshAll = useMutation({
    mutationFn: () => api.post<BatchProbeResponse>("/api/egress/nodes/probe", { all: true }),
    onSuccess: (result) => {
      if (result.mode !== "async") return;
      invalidate(queryClient);
      setProbeJob(result.job);
      toast.success(result.started ? "全量刷新已开始" : "已有刷新任务在进行，已接入进度");
    },
  });
  const cancelProbe = useMutation({
    mutationFn: () => api.post<{ cancelled: boolean }>("/api/egress/nodes/probe/cancel"),
    onSuccess: (result) => {
      toast.success(result.cancelled ? "已请求取消刷新" : "当前没有运行中的刷新任务");
    },
  });
  const jobStatus = useQuery({
    queryKey: ["egress", "probe-job"],
    queryFn: () => api.get<{ job: BatchProbeJob }>("/api/egress/nodes/probe/status"),
    // Always fetch once on mount so leaving the page and coming back
    // re-attaches to a job that is still running on the server.
    refetchInterval: (current) => (current.state.data?.job?.running ? 1_500 : false),
  });
  useEffect(() => {
    const job = jobStatus.data?.job;
    if (!job?.id) return;
    setProbeJob(job);
    if (job.running) {
      observedRunning.current = job.id;
      return;
    }
    if (observedRunning.current !== job.id) return;
    observedRunning.current = null;
    invalidate(queryClient);
    toast.success(job.cancelled ? `已取消刷新：${job.done}/${job.total}` : `批量刷新完成：${job.done}/${job.total}`);
    setSelected(new Set());
  }, [jobStatus.data, queryClient, toast]);

  if (query.isLoading && !query.data) {
    return <Loading label="读取节点池…" />;
  }
  if (query.isError) {
    return <ErrorNotice message={formatApiErrorMessage(query.error)} />;
  }
  const page = query.data;
  if (!page) return <EmptyState />;
  if (!page.enabled) {
    return (
      <>
        <PageHeader eyebrow="Egress" title="代理节点池" description="统一出口调度。当前未启用。" />
        <EmptyState title="出口池未启用" hint="在代理订阅或系统设置中启用 egress" />
      </>
    );
  }

  const names = page.subscription_names ?? {};
  const nodes = page.nodes ?? [];
  const batchTargets = selected.size > 0 ? Array.from(selected) : nodes.map((node) => node.id);
  const batchBusy = batchProbe.isPending || refreshAll.isPending || Boolean(probeJob?.running);
  const totalPages = Math.max(1, Math.ceil(page.total / filters.limit));
  const currentPage = Math.floor(filters.offset / filters.limit) + 1;
  const dirty =
    filters.keyword !== "" ||
    filters.status !== "all" ||
    filters.subscription !== "all" ||
    filters.protocol !== "all" ||
    filters.region !== "all" ||
    filters.sort_by !== defaultFilters.sort_by;

  const sortBy = (column: string) =>
    patch({
      sort_by: column,
      sort_order: filters.sort_by === column && filters.sort_order === "desc" ? "asc" : "desc",
      offset: 0,
    });

  const columns: DataColumn<EgressNode>[] = [
    {
      key: "name",
      header: "节点",
      sortKey: "name",
      cell: (node) => (
        <div>
          <button className="link-button" onClick={() => setDetailId(node.id)}>
            {node.name}
          </button>
          <div className="muted mono">{node.id.slice(0, 12)}…</div>
        </div>
      ),
    },
    {
      key: "protocol",
      header: "协议",
      cell: (node) => (
        <div>
          <span className="tag">{node.protocol}</span>
          {node.raw_type && node.raw_type !== node.protocol ? <div className="muted">{node.raw_type}</div> : null}
        </div>
      ),
    },
    {
      key: "region",
      header: "地区",
      cell: (node) =>
        node.region ? (
          <div>
            <span title={node.region}>{flagEmoji(node.region)}</span> {node.region}
            {node.region_name ? <div className="muted">{node.region_name}</div> : null}
          </div>
        ) : (
          <span className="muted">-</span>
        ),
    },
    {
      key: "subscriptions",
      header: "订阅与别名",
      cell: (node) => (
        <div>
          {(node.subscriptions ?? []).length === 0
            ? "-"
            : (node.subscriptions ?? []).map((id) => (
                <div key={id} className="muted">
                  {names[id] ?? id}
                  {aliasFor(node, id) ? <span className="mono"> · {aliasFor(node, id)}</span> : null}
                </div>
              ))}
        </div>
      ),
    },
    {
      key: "status",
      header: "状态",
      cell: (node) => (
        <div>
          <span className={`badge ${statusTone[node.status] ?? "badge-neutral"}`}>
            {statusLabels[node.status] ?? node.status}
          </span>
          {node.eligible ? <div className="muted">可调度</div> : null}
        </div>
      ),
    },
    {
      key: "health_state",
      header: "健康态",
      cell: (node) => (
        <div>
          <span className={`badge ${healthTone[node.health_state ?? ""] ?? "badge-neutral"}`}>
            {healthLabels[node.health_state ?? ""] ?? node.health_state ?? "无证据"}
          </span>
          {node.http200_lower != null ? (
            <div className="muted">HTTP {formatPercent(node.http200_lower, 1)}</div>
          ) : null}
          {node.valid_lower != null ? <div className="muted">推理 {formatPercent(node.valid_lower, 1)}</div> : null}
        </div>
      ),
    },
    {
      key: "health",
      header: "健康分",
      sortKey: "health",
      cell: (node) => formatPercent(node.health_score, 1),
    },
    {
      key: "latency",
      header: "延迟",
      sortKey: "latency",
      cell: (node) => (
        <span className={latencyClass(node)}>{node.has_latency ? `${Math.round(node.latency_ms ?? 0)} ms` : "-"}</span>
      ),
    },
    {
      key: "score",
      header: "综合分",
      sortKey: "score",
      cell: (node) => (
        <div>
          <strong>{(node.score?.total ?? 0).toFixed(2)}</strong>
          <div className="muted">
            H {(node.score?.health ?? 0).toFixed(2)} · L {(node.score?.latency ?? 0).toFixed(2)} · U{" "}
            {(node.score?.upstream ?? 0).toFixed(2)}
          </div>
        </div>
      ),
    },
    {
      key: "failures",
      header: "连续失败",
      sortKey: "failures",
      align: "right",
      cell: (node) => node.failures || "-",
    },
    {
      key: "last_probe",
      header: "上次探测",
      sortKey: "last_probe_at",
      cell: (node) => (node.last_probe_at ? formatRelativeTime(node.last_probe_at) : "未探测"),
    },
    {
      key: "cooldown",
      header: "冷却至",
      cell: (node) => (node.cooldown_until ? formatDateTime(node.cooldown_until) : "-"),
    },
    {
      key: "created",
      header: "创建时间",
      sortKey: "created_at",
      cell: (node) => formatDateTime(node.created_at),
    },
    {
      key: "enabled",
      header: "启用",
      cell: (node) => (
        <Switch
          checked={node.enabled}
          disabled={toggle.isPending}
          onChange={(event) => toggle.mutate({ id: node.id, enabled: event.target.checked })}
          aria-label={`切换 ${node.name}`}
        />
      ),
    },
    {
      key: "actions",
      header: "操作",
      cell: (node) => (
        <div className="table-actions">
          <Button size="sm" variant="secondary" onClick={() => setDetailId(node.id)}>
            详情
          </Button>
          <Button size="sm" variant="secondary" disabled={probe.isPending} onClick={() => probe.mutate(node.id)}>
            <Zap size={13} /> 探测
          </Button>
        </div>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Egress"
        title="代理节点池"
        description="全量节点按条件分页展示；健康、延迟与综合分来自出口状态库与实时调度。"
        actions={
          <>
            {dirty ? (
              <Button
                variant="ghost"
                onClick={() => {
                  setKeyword("");
                  patch({ ...defaultFilters });
                }}
              >
                <X size={14} /> 重置筛选
              </Button>
            ) : null}
            <Button variant="secondary" onClick={() => invalidate(queryClient)}>
              <RefreshCw size={15} /> 刷新列表
            </Button>
          </>
        }
      />

      <div className="stats-grid">
        <StatCard label="节点总数" value={page.pool} hint={`当前筛选 ${page.total} 个`} />
        <StatCard
          label="可调度节点"
          value={page.counts.eligible}
          tone={page.counts.eligible > 0 ? "success" : "danger"}
          hint="进入候选池且未被排除"
        />
        <StatCard label="冷却中" value={page.counts.cooling} tone={page.counts.cooling ? "warning" : undefined} hint="等待冷却结束" />
        <StatCard label="停用 / 不支持" value={`${page.counts.disabled} / ${page.counts.unsupported}`} hint="保留展示，不参与调度" />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>筛选与排序</h3>
          <span className="badge badge-neutral">筛选条件写入地址栏，可直接分享</span>
        </div>
        <FilterBar>
          <SearchField
            value={keyword}
            onChange={setKeyword}
            placeholder="名称 / 别名 / ID / 地址 / 订阅"
            ariaLabel="搜索节点"
          />
          <Select
            value={filters.status}
            onChange={(event) => patch({ status: event.target.value, offset: 0 })}
            aria-label="按状态筛选"
            style={{ width: 140 }}
          >
            <option value="all">全部状态</option>
            {(page.facets?.statuses ?? []).map((item) => (
              <option key={item} value={item}>
                {statusLabels[item] ?? item}
              </option>
            ))}
          </Select>
          <Select
            value={filters.subscription}
            onChange={(event) => patch({ subscription: event.target.value, offset: 0 })}
            aria-label="按订阅筛选"
            style={{ width: 160 }}
          >
            <option value="all">全部订阅</option>
            {(page.facets?.subscriptions ?? []).map((item) => (
              <option key={item} value={item}>
                {names[item] ? `${names[item]}（${item}）` : item}
              </option>
            ))}
          </Select>
          <Select
            value={filters.protocol}
            onChange={(event) => patch({ protocol: event.target.value, offset: 0 })}
            aria-label="按协议筛选"
            style={{ width: 120 }}
          >
            <option value="all">全部协议</option>
            {(page.facets?.protocols ?? []).map((item) => (
              <option key={item} value={item}>
                {item}
              </option>
            ))}
          </Select>
          <Select
            value={filters.region}
            onChange={(event) => patch({ region: event.target.value, offset: 0 })}
            aria-label="按地区筛选"
            disabled={(page.facets?.regions ?? []).length === 0}
            style={{ width: 150 }}
          >
            <option value="all">
              {(page.facets?.regions ?? []).length === 0
                ? page.facets?.geoip_enabled
                  ? "全部地区（暂无归属）"
                  : "未启用 GeoIP"
                : "全部地区"}
            </option>
            {(page.facets?.regions ?? []).map((item) => (
              <option key={item} value={item}>
                {flagEmoji(item)} {item}
              </option>
            ))}
          </Select>
          <Select
            value={String(filters.limit)}
            onChange={(event) => patch({ limit: Number(event.target.value), offset: 0 })}
            aria-label="每页条数"
            style={{ width: 110 }}
          >
            {pageSizes.map((size) => (
              <option key={size} value={size}>
                每页 {size}
              </option>
            ))}
          </Select>
        </FilterBar>
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>节点列表</h3>
          <div className="field-inline selection-bar">
            <span className="badge badge-neutral">
              第 {currentPage}/{totalPages} 页 · 共 {page.total} 个
            </span>
            {selected.size > 0 ? <span className="badge badge-info">已选 {selected.size}</span> : null}
            {selected.size > 0 ? (
              <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())} disabled={batchBusy}>
                清空选择
              </Button>
            ) : null}
            {probeJob?.running ? (
              <>
                <span className="badge badge-info">
                  刷新中 {probeJob.done}/{probeJob.total}
                </span>
                <Button size="sm" variant="ghost" onClick={() => cancelProbe.mutate()} disabled={cancelProbe.isPending}>
                  取消
                </Button>
              </>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => refreshAll.mutate()}
                  disabled={batchBusy}
                  title="对池内全部节点执行 TCP 预检 + 探测，后台运行并可取消"
                >
                  <RefreshCw size={13} /> 刷新全部
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => batchProbe.mutate(batchTargets)}
                  disabled={batchBusy || batchTargets.length === 0}
                  title={
                    selected.size > 0
                      ? `重新探测所选 ${selected.size} 个节点`
                      : `重新探测当前页 ${nodes.length} 个节点；超过 ${200} 个自动转为后台任务`
                  }
                >
                  <RefreshCw size={13} />
                  {selected.size > 0 ? `刷新所选 ${selected.size}` : "刷新本页"}
                </Button>
              </>
            )}
          </div>
        </div>
        <DataTable
          columns={columns}
          rows={nodes}
          rowKey={(node) => node.id}
          loading={query.isLoading}
          sort={{ key: filters.sort_by, order: filters.sort_order }}
          onSort={sortBy}
          emptyTitle="没有匹配的节点"
          emptyHint="调整筛选条件，或先在订阅页添加订阅"
          caption="代理节点列表"
          minWidth={1314}
          maxHeight={340}
          selection={{ selected, onChange: setSelected, disabled: batchBusy }}
        />
        <div className="pager-row">
          <Pagination
            page={currentPage}
            pageSize={filters.limit}
            total={page.total}
            onPrev={() => patch({ offset: Math.max(0, filters.offset - filters.limit) })}
            onNext={() => patch({ offset: filters.offset + filters.limit })}
            label={`显示 ${page.total === 0 ? 0 : filters.offset + 1}-${Math.min(filters.offset + filters.limit, page.total)} / ${page.total}`}
          />
          <div className="field-inline">
            <span className="muted">跳转到第</span>
            <Input
              type="number"
              min={1}
              max={totalPages}
              defaultValue={currentPage}
              key={currentPage}
              style={{ width: 76 }}
              onKeyDown={(event) => {
                if (event.key !== "Enter") return;
                const target = Number((event.target as HTMLInputElement).value);
                if (!Number.isFinite(target)) return;
                const next = Math.min(Math.max(1, Math.trunc(target)), totalPages);
                patch({ offset: (next - 1) * filters.limit });
              }}
            />
            <span className="muted">/ {totalPages} 页</span>
          </div>
        </div>
        {toggle.isError || probe.isError || batchProbe.isError || refreshAll.isError || cancelProbe.isError || detail.isError ? (
          <ErrorNotice
            message={formatApiErrorMessage(
              toggle.error ?? probe.error ?? batchProbe.error ?? refreshAll.error ?? cancelProbe.error ?? detail.error,
            )}
          />
        ) : null}
      </div>

      <GeoIPPanel enabled={Boolean(page.facets?.geoip_enabled)} />

      <Drawer
        open={Boolean(detailId)}
        onClose={() => setDetailId(null)}
        title={detail.data?.node.name ?? "节点详情"}
        subtitle={detailId ?? undefined}
        size="lg"
        actions={
          <>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => navigate(`/requests?exit=${encodeURIComponent(detailId ?? "")}`)}
            >
              查看该节点请求
            </Button>
            <Button size="sm" variant="secondary" disabled={probe.isPending} onClick={() => probe.mutate(detailId ?? "")}>
              <Zap size={13} /> 立即探测
            </Button>
          </>
        }
      >
        {detail.data?.node ? <NodeDetail node={detail.data.node} names={names} /> : <Loading label="读取节点详情…" />}
      </Drawer>
    </>
  );
}

// geoipMirrors are the GitHub accelerators offered for the country database.
// The official release URL is kept first so a fresh install uses it directly.
const geoipMirrors = [
  {
    id: "direct",
    label: "GitHub 官方",
    url: "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
  },
  {
    id: "jsdelivr",
    label: "jsDelivr（推荐）",
    url: "https://fastly.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb",
  },
  {
    id: "ghproxy",
    label: "ghproxy 镜像",
    url: "https://mirror.ghproxy.com/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
  },
  {
    id: "ghfast",
    label: "ghfast.top 加速",
    url: "https://ghfast.top/https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/country.mmdb",
  },
];

function GeoIPPanel({ enabled }: { enabled: boolean }) {
  const queryClient = useQueryClient();
  const [probe, setProbe] = useState("");
  const [answer, setAnswer] = useState<GeoIPLookupResponse | null>(null);
  const [source, setSource] = useState("");
  const [customURL, setCustomURL] = useState("");
  const status = useQuery({
    queryKey: ["egress", "geoip"],
    queryFn: () => api.get<GeoIPStatusResponse>("/api/egress/geoip"),
    enabled,
    refetchInterval: 60_000,
  });
  const update = useMutation({
    mutationFn: () => api.post<GeoIPStatusResponse>("/api/egress/geoip/update"),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["egress", "geoip"] }),
  });
  const lookup = useMutation({
    mutationFn: (ip: string) => api.get<GeoIPLookupResponse>(`/api/egress/geoip/lookup?ip=${encodeURIComponent(ip)}`),
    onSuccess: (data) => setAnswer(data),
  });
  const config = useQuery({
    queryKey: ["config"],
    queryFn: () => api.get<ConfigView>("/api/config"),
    enabled,
    staleTime: 30_000,
  });
  const applySource = useMutation({
    mutationFn: async (url: string) => {
      const cfg = config.data;
      if (!cfg || !cfg.egress) throw new Error("配置尚未加载");
      const merged = {
        ...cfg.egress,
        geoip: { ...(cfg.egress.geoip ?? {}), enabled: true, url },
      };
      await api.put("/api/config", configUpdateFromView(cfg, merged));
      return api.post<GeoIPStatusResponse>("/api/egress/geoip/update");
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["egress", "geoip"] });
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
  });

  if (!enabled) {
    return (
      <div className="card panel">
        <div className="panel-head">
          <h3>地区解析（GeoIP）</h3>
          <span className="badge badge-muted">未启用</span>
        </div>
        <p className="muted">
          在配置的 <span className="mono">egress.geoip</span> 中启用后，节点会显示所属国家并可按地区筛选。
        </p>
      </div>
    );
  }

  const info = status.data?.status;
  const currentURL = info?.url ?? "";
  const selectedSource = source || geoipMirrors.find((mirror) => mirror.url === currentURL)?.id || "custom";
  const selectedMirror = geoipMirrors.find((mirror) => mirror.id === selectedSource);
  const applyURL = selectedSource === "custom" ? customURL.trim() : selectedMirror?.url ?? "";
  return (
    <div className="card panel">
      <div className="panel-head">
        <h3>地区解析（GeoIP）</h3>
        <span className={`badge ${info?.loaded ? "badge-success" : "badge-warning"}`}>
          {info?.loaded ? "已加载" : "未加载"}
        </span>
      </div>
      <dl className="geoip-info">
        <div>
          <dt>库文件</dt>
          <dd className="mono" title={info?.path ?? ""}>
            {info?.path ?? "-"}
          </dd>
        </div>
        <div>
          <dt>来源</dt>
          <dd className="mono" title={info?.url ?? ""}>
            {info?.url ?? "-"}
          </dd>
        </div>
        <div>
          <dt>大小</dt>
          <dd>{info?.size_bytes ? `${(info.size_bytes / 1024 / 1024).toFixed(1)} MiB` : "-"}</dd>
        </div>
        <div>
          <dt>SHA256</dt>
          <dd className="mono" title={info?.sha256 ?? ""}>
            {info?.sha256 ? `${info.sha256.slice(0, 16)}…` : "-"}
          </dd>
        </div>
        <div>
          <dt>最近更新</dt>
          <dd>{info?.last_update ? formatDateTime(info.last_update) : "从未"}</dd>
        </div>
        <div>
          <dt>下次更新</dt>
          <dd>{info?.next_update ? formatDateTime(info.next_update) : "-"}</dd>
        </div>
        <div>
          <dt>刷新周期</dt>
          <dd>{info?.refresh_days ?? "-"} 天</dd>
        </div>
        <div>
          <dt>错误</dt>
          <dd title={info?.last_error ?? ""}>{info?.last_error || "-"}</dd>
        </div>
      </dl>
      <div className="geoip-controls">
        <span className="muted">下载源</span>
        <Select
          value={selectedSource}
          onChange={(event) => {
            const value = event.target.value;
            setSource(value);
            if (value === "custom" && !customURL) {
              setCustomURL(currentURL);
            }
          }}
          aria-label="GeoIP 下载源"
          style={{ width: 190 }}
        >
          <option value="custom">自定义 URL</option>
          {geoipMirrors.map((mirror) => (
            <option key={mirror.id} value={mirror.id}>
              {mirror.label}
            </option>
          ))}
        </Select>
        {selectedSource === "custom" ? (
          <Input
            value={customURL}
            onChange={(event) => setCustomURL(event.target.value)}
            placeholder={currentURL || "https://example.com/country.mmdb"}
            aria-label="自定义 GeoIP 下载地址"
            style={{ width: 340 }}
          />
        ) : null}
        <Button
          variant="secondary"
          disabled={!applyURL.startsWith("http") || applySource.isPending}
          onClick={() => applySource.mutate(applyURL)}
        >
          {applySource.isPending ? "更新中…" : "应用并更新"}
        </Button>
      </div>
      <div className="geoip-controls">
        <Button variant="secondary" disabled={update.isPending} onClick={() => update.mutate()}>
          <RefreshCw size={14} /> 立即更新库
        </Button>
        <Input
          value={probe}
          onChange={(event) => setProbe(event.target.value)}
          placeholder="查询 IP 归属，如 1.1.1.1"
          style={{ width: 220 }}
        />
        <Button variant="secondary" disabled={!probe.trim() || lookup.isPending} onClick={() => lookup.mutate(probe.trim())}>
          查询
        </Button>
      </div>
      {answer ? (
        <p className="muted">
          <span className="mono">{answer.ip}</span>{" "}
          {answer.found ? (
            <>
              {flagEmoji(answer.country ?? "")} {answer.country} {answer.name}
            </>
          ) : (
            "未找到归属"
          )}
        </p>
      ) : null}
      {update.isError || lookup.isError || applySource.isError ? (
        <ErrorNotice message={formatApiErrorMessage(update.error ?? lookup.error ?? applySource.error)} />
      ) : null}
    </div>
  );
}

function NodeDetail({ node, names }: { node: EgressNode; names: Record<string, string> }) {
  const aliases = node.aliases?.length
    ? node.aliases
    : (node.subscriptions ?? []).map((id) => ({ subscription: id, tag: "" }));
  return (
    <div className="detail-stack">
      <section className="detail-section">
        <div className="detail-section-head">
          <h4>节点状态</h4>
          <p>
            <span className={`badge ${statusTone[node.status] ?? "badge-neutral"}`}>
              {statusLabels[node.status] ?? node.status}
            </span>{" "}
            {node.eligible ? "当前参与调度" : "当前不参与调度"}
          </p>
        </div>
        <dl className="node-detail-list">
          <div>
            <dt>协议</dt>
            <dd>
              {node.protocol}
              {node.raw_type && node.raw_type !== node.protocol ? `（原始 ${node.raw_type}）` : ""}
            </dd>
          </div>
          <div>
            <dt>地区</dt>
            <dd>
              {node.region ? `${flagEmoji(node.region)} ${node.region}${node.region_name ? ` · ${node.region_name}` : ""}` : "-"}
            </dd>
          </div>
          <div>
            <dt>地址</dt>
            <dd className="mono">
              {node.server ? `${node.server}${node.port ? `:${node.port}` : ""}` : node.protocol === "direct" ? "本机直连" : "-"}
              {node.tls ? " · tls" : ""}
            </dd>
          </div>
          <div>
            <dt>出口 IP</dt>
            <dd className="mono">{node.egress_ip || "-"}</dd>
          </div>
          <div>
            <dt>首次连通</dt>
            <dd>{node.ever_healthy ? "已确认" : "待首次探测"}</dd>
          </div>
          <div>
            <dt>健康分</dt>
            <dd>{formatPercent(node.health_score, 1)}</dd>
          </div>
          <div>
            <dt>参考延迟</dt>
            <dd>{node.has_latency ? `${Math.round(node.latency_ms ?? 0)} ms` : "-"}</dd>
          </div>
          <div>
            <dt>连续失败</dt>
            <dd>{node.failures}</dd>
          </div>
          <div>
            <dt>上次探测</dt>
            <dd>
              {node.last_probe_at ? `${formatRelativeTime(node.last_probe_at)}（${formatDateTime(node.last_probe_at)}）` : "未探测"}
            </dd>
          </div>
          <div>
            <dt>上次成功</dt>
            <dd>{node.last_success_at ? formatRelativeTime(node.last_success_at) : "从未"}</dd>
          </div>
          <div>
            <dt>冷却至</dt>
            <dd>{node.cooldown_until ? formatDateTime(node.cooldown_until) : "-"}</dd>
          </div>
          <div>
            <dt>熔断起点</dt>
            <dd>{node.cooldown_since ? formatDateTime(node.cooldown_since) : "-"}</dd>
          </div>
          <div>
            <dt>创建时间</dt>
            <dd>{formatDateTime(node.created_at)}</dd>
          </div>
          <div>
            <dt>手动启用</dt>
            <dd>{node.enabled ? "是" : "否"}</dd>
          </div>
          <div>
            <dt>支持调度</dt>
            <dd>{node.supported ? "是" : "否"}</dd>
          </div>
          <div>
            <dt>最近错误</dt>
            <dd className="pre-wrap">{node.last_error || "-"}</dd>
          </div>
        </dl>
      </section>

      <section className="detail-section">
        <div className="detail-section-head">
          <h4>健康键</h4>
          <p>按「上游 × 模型 × 协议 × 模式」统计的准入证据；样本不足时下界为空</p>
        </div>
        {node.health_keys && node.health_keys.length > 0 ? (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>上游 / 模型</th>
                  <th>协议 / 模式</th>
                  <th>状态</th>
                  <th>样本</th>
                  <th>HTTP 下界</th>
                  <th>推理下界</th>
                  <th>TTFB p95</th>
                </tr>
              </thead>
              <tbody>
                {node.health_keys.map((key) => (
                  <tr key={`${key.target}-${key.model}-${key.protocol}-${key.mode}`}>
                    <td>
                      <div className="mono">{key.model}</div>
                      <div className="muted">{key.target}</div>
                    </td>
                    <td>
                      <span className="tag">{key.protocol}</span> <span className="muted">{key.mode}</span>
                    </td>
                    <td>
                      <span className={`badge ${healthTone[key.state] ?? "badge-neutral"}`}>
                        {healthLabels[key.state] ?? key.state}
                      </span>
                      {key.pending_reverify ? <div className="muted">待复验 {key.reverify_success}</div> : null}
                    </td>
                    <td>
                      {key.samples}
                      <div className="muted">
                        {key.primary_success}/{key.primary_failure}
                      </div>
                    </td>
                    <td>{key.http200_lower == null ? "证据不足" : formatPercent(key.http200_lower, 1)}</td>
                    <td>{key.valid_lower == null ? "证据不足" : formatPercent(key.valid_lower, 1)}</td>
                    <td>{key.ttfb_p95_ms > 0 ? `${key.ttfb_p95_ms} ms` : "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted">该节点暂无健康键证据。</p>
        )}
      </section>

      <section className="detail-section">
        <div className="detail-section-head">
          <h4>调度评分</h4>
          <p>针对主上游目标实时计算的 H / L / R / U 分项</p>
        </div>
        <dl className="node-detail-list">
          <div>
            <dt>综合</dt>
            <dd>{(node.score?.total ?? 0).toFixed(3)}</dd>
          </div>
          <div>
            <dt>健康 H</dt>
            <dd>{(node.score?.health ?? 0).toFixed(3)}</dd>
          </div>
          <div>
            <dt>延迟 L</dt>
            <dd>{(node.score?.latency ?? 0).toFixed(3)}</dd>
          </div>
          <div>
            <dt>路由 R</dt>
            <dd>{(node.score?.routing ?? 0).toFixed(3)}</dd>
          </div>
          <div>
            <dt>上游成功率 U</dt>
            <dd>{(node.score?.upstream ?? 0).toFixed(3)}</dd>
          </div>
        </dl>
      </section>

      <section className="detail-section">
        <div className="detail-section-head">
          <h4>订阅别名</h4>
          <p>同一出口在多个订阅里的名字全部列出</p>
        </div>
        {aliases.length === 0 ? (
          <EmptyState title="无订阅引用" />
        ) : (
          <div className="timeline">
            {aliases.map((alias) => (
              <div className="timeline-item" key={alias.subscription}>
                <span className="tag">{names[alias.subscription] ?? alias.subscription}</span>
                <span className="mono">{alias.tag || node.name}</span>
                <span className="muted mono">{alias.subscription}</span>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function aliasFor(node: EgressNode, subscription: string): string {
  return node.aliases?.find((item) => item.subscription === subscription)?.tag ?? "";
}

function latencyClass(node: EgressNode): string {
  if (!node.has_latency) return "muted";
  const latency = node.latency_ms ?? 0;
  if (latency < 400) return "badge badge-success";
  if (latency < 1000) return "badge badge-warning";
  return "badge badge-danger";
}

function queryString(filters: Filters): string {
  const params = new URLSearchParams();
  if (filters.keyword) params.set("keyword", filters.keyword);
  if (filters.status !== "all") params.set("status", filters.status);
  if (filters.subscription !== "all") params.set("subscription", filters.subscription);
  if (filters.protocol !== "all") params.set("protocol", filters.protocol);
  if (filters.region !== "all") params.set("region", filters.region);
  params.set("sort_by", filters.sort_by);
  params.set("sort_order", filters.sort_order);
  params.set("offset", String(filters.offset));
  params.set("limit", String(filters.limit));
  return params.toString();
}

function invalidate(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ["egress", "nodes"] });
  queryClient.invalidateQueries({ queryKey: ["egress", "node"] });
  queryClient.invalidateQueries({ queryKey: ["egress", "summary"] });
}
