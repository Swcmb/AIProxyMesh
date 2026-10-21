import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Pencil, Plus, RefreshCw, Save, Trash2, Wand2 } from "lucide-react";
import { api } from "../lib/api";
import type { ConfigView, EgressSnapshot, EgressSubscription, SubscriptionPreset } from "../lib/types";
import { ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Switch } from "../components/ui/Switch";
import { Textarea } from "../components/ui/Textarea";
import { useToast } from "../components/ui/Toast";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

type SourceKind = "url" | "content";

type Draft = {
  id: string;
  name: string;
  kind: SourceKind;
  url: string;
  content: string;
  enabled: boolean;
  refresh_seconds: number;
  ephemeral: boolean;
  evict_delay_seconds: number;
};

const emptyDraft: Draft = {
  id: "",
  name: "",
  kind: "url",
  url: "",
  content: "",
  enabled: true,
  refresh_seconds: 3600,
  ephemeral: false,
  evict_delay_seconds: 600,
};

function draftPayload(draft: Draft): Record<string, unknown> {
  const body: Record<string, unknown> = {
    enabled: draft.enabled,
    name: draft.name.trim(),
    ephemeral: draft.ephemeral,
    evict_delay_seconds: draft.ephemeral ? draft.evict_delay_seconds : 0,
  };
  if (draft.kind === "url") {
    body.url = draft.url.trim();
    body.content = "";
    body.refresh_seconds = draft.refresh_seconds;
  } else {
    body.content = draft.content;
    body.url = "";
    body.refresh_seconds = 0;
  }
  return body;
}

export function SubscriptionsPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<{ id: string; url: string; content: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  const config = useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/api/config") });
  const egress = useQuery({
    queryKey: ["egress", "summary"],
    queryFn: () => api.get<EgressSnapshot>("/api/egress"),
    refetchInterval: 15_000,
  });
  const presets = useQuery({
    queryKey: ["egress", "subscription-presets"],
    queryFn: () => api.get<{ presets: SubscriptionPreset[] }>("/api/egress/subscription-presets"),
    staleTime: 300_000,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["config"] });
    queryClient.invalidateQueries({ queryKey: ["egress"] });
  };

  const create = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.post("/api/egress/subscriptions", body),
    onSuccess: (_data, body) => {
      invalidate();
      setDraft(emptyDraft);
      toast.success(`订阅 ${String(body.id ?? "")} 已添加`);
    },
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) =>
      api.patch(`/api/egress/subscriptions/${id}`, body),
    onSuccess: (_data, variables) => {
      invalidate();
      setEditingId(null);
      setDraft(emptyDraft);
      toast.success(`订阅 ${variables.id} 已保存`);
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/egress/subscriptions/${id}`),
    onSuccess: (_data, id) => {
      invalidate();
      setDeleteTarget(null);
      if (revealed?.id === id) setRevealed(null);
      toast.success(`订阅 ${id} 已删除`);
    },
  });
  const refresh = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/subscriptions/${id}/refresh`),
    onSuccess: (_data, id) => {
      invalidate();
      toast.success(`订阅 ${id} 已刷新`);
    },
  });
  const cleanup = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/subscriptions/${id}/cleanup`),
    onSuccess: (_data, id) => {
      invalidate();
      toast.success(`订阅 ${id} 的冷却状态已清理`);
    },
  });
  const reveal = useMutation({
    mutationFn: async (id: string) =>
      api.get<{ subscription: { url?: string; content?: string } }>(`/api/egress/subscriptions/${id}`),
    onSuccess: (data, id) =>
      setRevealed({ id, url: data.subscription?.url ?? "", content: data.subscription?.content ?? "" }),
  });

  const busy = create.isPending || update.isPending || remove.isPending || cleanup.isPending;
  const subscriptions = egress.data?.subscriptions ?? [];
  const configSubscriptions = config.data?.egress?.subscriptions ?? [];
  const error = create.error ?? update.error ?? remove.error ?? cleanup.error ?? reveal.error;

  const stats = useMemo(
    () => ({
      total: subscriptions.length,
      enabled: subscriptions.filter((sub) => sub.enabled).length,
      nodes: subscriptions.reduce((sum, sub) => sum + sub.nodes, 0),
      unsupported: subscriptions.reduce((sum, sub) => sum + sub.unsupported, 0),
    }),
    [subscriptions],
  );

  const startEdit = (id: string) => {
    const entry = configSubscriptions.find((sub) => sub.id === id);
    if (!entry) return;
    setEditingId(id);
    setDraft({
      id: entry.id,
      name: entry.name ?? "",
      kind: entry.inline ? "content" : "url",
      url: entry.url ?? "",
      content: "",
      enabled: entry.enabled,
      refresh_seconds: entry.refresh_seconds ?? 0,
      ephemeral: entry.ephemeral ?? false,
      evict_delay_seconds: entry.evict_delay_seconds || 600,
    });
  };

  const submit = () => {
    const id = draft.id.trim();
    if (!id) return;
    if (editingId) {
      update.mutate({ id: editingId, body: draftPayload(draft) });
    } else {
      create.mutate({ id, ...draftPayload(draft) });
    }
  };

  const columns: DataColumn<EgressSubscription>[] = [
    {
      key: "subscription",
      header: "订阅",
      cell: (sub) => (
        <div>
          <strong>{sub.name || sub.id}</strong>
          <div className="muted mono">{sub.id}</div>
          {sub.ephemeral ? (
            <span className="tag" title="连续熔断超过该时长后从本订阅驱逐">
              临时 · 驱逐 {sub.evict_delay_seconds ?? 0}s
            </span>
          ) : null}
        </div>
      ),
    },
    {
      key: "source",
      header: "来源",
      cell: (sub) => {
        const entry = configSubscriptions.find((item) => item.id === sub.id);
        return sub.inline ? (
          <span className="badge badge-neutral">粘贴 {entry?.content_lines ?? 0} 行</span>
        ) : (
          <span className="mono">{sub.url ?? "-"}</span>
        );
      },
    },
    { key: "format", header: "格式", cell: (sub) => sub.format || "-" },
    {
      key: "nodes",
      header: "节点数",
      cell: (sub) => (
        <div>
          {sub.nodes}
          <div className="muted">
            支持 {sub.supported} · 不支持 {sub.unsupported}
          </div>
        </div>
      ),
    },
    {
      key: "changes",
      header: "新增 / 移除",
      cell: (sub) => `${sub.added} / ${sub.removed}`,
    },
    { key: "last", header: "最近拉取", cell: (sub) => (sub.last_fetch_at ? formatDateTime(sub.last_fetch_at) : "-") },
    { key: "next", header: "下次拉取", cell: (sub) => (sub.next_fetch_at ? formatDateTime(sub.next_fetch_at) : "手动") },
    {
      key: "error",
      header: "错误",
      cell: (sub) => <span className="pre-wrap">{sub.last_error || "-"}</span>,
    },
    {
      key: "enabled",
      header: "启用",
      cell: (sub) => {
        const entry = configSubscriptions.find((item) => item.id === sub.id);
        return (
          <Switch
            checked={sub.enabled}
            disabled={!entry || update.isPending}
            onChange={(event) => update.mutate({ id: sub.id, body: { enabled: event.target.checked } })}
            aria-label={`切换 ${sub.name || sub.id}`}
          />
        );
      },
    },
    {
      key: "actions",
      header: "操作",
      cell: (sub) => {
        const entry = configSubscriptions.find((item) => item.id === sub.id);
        return (
          <div className="table-actions">
            {entry ? (
              <Button size="sm" variant="secondary" onClick={() => startEdit(sub.id)}>
                <Pencil size={13} /> 编辑
              </Button>
            ) : null}
            <Button size="sm" variant="secondary" disabled={reveal.isPending} onClick={() => reveal.mutate(sub.id)}>
              <Eye size={13} /> 原文
            </Button>
            <Button
              size="sm"
              variant="secondary"
              disabled={refresh.isPending}
              onClick={() => refresh.mutate(sub.id)}
            >
              <RefreshCw size={13} /> 刷新
            </Button>
            <Button size="sm" variant="secondary" disabled={busy} onClick={() => cleanup.mutate(sub.id)}>
              <Wand2 size={13} /> 清理冷却
            </Button>
            {entry ? (
              <Button size="sm" variant="danger" disabled={busy} onClick={() => setDeleteTarget(sub.id)}>
                <Trash2 size={13} /> 删除
              </Button>
            ) : null}
          </div>
        );
      },
    },
  ];

  if ((config.isLoading && !config.data) || (egress.isLoading && !egress.data)) {
    return <Loading label="读取订阅…" />;
  }
  if (config.isError) {
    return <ErrorNotice message={formatApiErrorMessage(config.error)} />;
  }

  return (
    <>
      <PageHeader
        eyebrow="Subscriptions"
        title="代理订阅"
        description="URL 订阅与粘贴清单统一按节点去重，健康状态跨订阅共享。"
        actions={
          <Button variant="secondary" onClick={invalidate}>
            <RefreshCw size={15} /> 刷新列表
          </Button>
        }
      />

      <div className="stats-grid">
        <StatCard label="订阅源" value={stats.total} hint={`${stats.enabled} 个已启用`} />
        <StatCard label="导入节点数" value={stats.nodes} hint="按订阅重复计数" />
        <StatCard label="不支持协议数" value={stats.unsupported} hint="可见但不入候选池" />
        <StatCard
          label="旧代理导入"
          value={config.data?.egress?.import_legacy_proxies ? "开" : "关"}
          hint="proxies / proxyfile"
        />
      </div>

      {!egress.data?.enabled ? <ErrorNotice message="出口池未启用：添加订阅保存后会自动启用 egress。" /> : null}

      {(presets.data?.presets.length ?? 0) > 0 ? (
        <div className="card panel">
          <div className="panel-head">
            <h3>预设</h3>
            <span className="badge badge-neutral">默认不启用，点击添加后才会拉取</span>
          </div>
          <div className="timeline">
            {(presets.data?.presets ?? []).map((preset) => {
              const added = subscriptions.some((sub) => sub.id === preset.id);
              return (
                <div className="timeline-item" key={preset.id}>
                  <div>
                    <strong>{preset.name}</strong>
                    <div className="muted mono">
                      {preset.id} · {preset.format} · {preset.protocols.join("/")}
                    </div>
                  </div>
                  <div>
                    <div>{preset.description}</div>
                    <div className="muted">{preset.risk}</div>
                  </div>
                  <div className="table-actions">
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={added || busy}
                      onClick={() =>
                        create.mutate({
                          id: preset.id,
                          name: preset.name,
                          url: preset.url,
                          enabled: true,
                          refresh_seconds: 3600,
                          protocols: preset.protocols,
                        })
                      }
                    >
                      <Wand2 size={13} /> {added ? "已添加" : "添加"}
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}

      <div className="card panel">
        <div className="panel-head">
          <h3>{editingId ? `编辑订阅：${editingId}` : "新增订阅"}</h3>
          <span className="badge badge-neutral">{editingId ? "只改这一条" : "保存后立即拉取"}</span>
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">ID</span>
            <Input
              value={draft.id}
              disabled={Boolean(editingId)}
              onChange={(event) => setDraft({ ...draft, id: event.target.value })}
              placeholder="sub-main"
            />
          </label>
          <label className="field-group">
            <span className="field-label">名称</span>
            <Input
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              placeholder="主订阅"
            />
          </label>
          <label className="field-group">
            <span className="field-label">来源</span>
            <Select value={draft.kind} onChange={(event) => setDraft({ ...draft, kind: event.target.value as SourceKind })}>
              <option value="url">URL 拉取</option>
              <option value="content">粘贴内容</option>
            </Select>
          </label>
          {draft.kind === "url" ? (
            <label className="field-group">
              <span className="field-label">订阅 URL</span>
              <Input
                value={draft.url}
                onChange={(event) => setDraft({ ...draft, url: event.target.value })}
                placeholder="https://example.com/sub"
              />
            </label>
          ) : (
            <label className="field-group field-span-2">
              <span className="field-label">节点清单</span>
              <Textarea
                value={draft.content}
                onChange={(event) => setDraft({ ...draft, content: event.target.value })}
                rows={4}
                placeholder={"http://1.2.3.4:8080#节点A\nsocks5://5.6.7.8:1080#节点B"}
              />
            </label>
          )}
          {draft.kind === "url" ? (
            <label className="field-group">
              <span className="field-label">刷新间隔（秒）</span>
              <Input
                type="number"
                min={0}
                value={draft.refresh_seconds}
                onChange={(event) => setDraft({ ...draft, refresh_seconds: Number(event.target.value) || 0 })}
              />
            </label>
          ) : null}
          <label className="field-group">
            <span className="field-label">启用</span>
            <Switch
              checked={draft.enabled}
              onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
              aria-label="启用该订阅"
            />
          </label>
          <label className="field-group">
            <span className="field-label">临时源（连续熔断驱逐）</span>
            <div className="field-inline">
              <Switch
                checked={draft.ephemeral}
                onChange={(event) => setDraft({ ...draft, ephemeral: event.target.checked })}
                aria-label="标记为临时源"
              />
              <Input
                type="number"
                min={60}
                max={86400}
                value={draft.evict_delay_seconds}
                disabled={!draft.ephemeral}
                onChange={(event) =>
                  setDraft({ ...draft, evict_delay_seconds: Number(event.target.value) || 60 })
                }
                aria-label="驱逐延迟秒数"
                style={{ width: 92 }}
              />
              <span className="muted">秒</span>
            </div>
          </label>
          <div className="field-inline" style={{ alignItems: "flex-end" }}>
            <Button onClick={submit} disabled={busy}>
              {editingId ? <Save size={15} /> : <Plus size={15} />} {editingId ? "保存修改" : "添加订阅"}
            </Button>
            {editingId ? (
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => {
                  setEditingId(null);
                  setDraft(emptyDraft);
                }}
              >
                取消
              </Button>
            ) : null}
          </div>
        </div>
        <p className="muted">
          编辑时保持 <span className="mono">scheme://host/***</span> 掩码即沿用已存地址；粘贴内容不参与定时刷新，保存后立即解析一次。
        </p>
        {error ? <ErrorNotice message={formatApiErrorMessage(error)} /> : null}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>订阅状态</h3>
          <span className="badge badge-neutral">{subscriptions.length}</span>
        </div>
        <DataTable
          columns={columns}
          rows={subscriptions}
          rowKey={(sub) => sub.id}
          loading={egress.isLoading}
          emptyTitle="暂无订阅"
          emptyHint="legacy 源会在启用 egress 后自动出现"
          caption="订阅状态"
          minWidth={1180}
        />
      </div>

      <Drawer
        open={Boolean(revealed)}
        onClose={() => setRevealed(null)}
        title={revealed ? `订阅原文：${revealed.id}` : "订阅原文"}
        subtitle="敏感内容：仅在必要时查看，请勿截图或分享。"
      >
        {revealed ? (
          <>
            {revealed.url ? (
              <div className="detail-section">
                <h4>来源 URL</h4>
                <p className="mono pre-wrap">{revealed.url}</p>
              </div>
            ) : null}
            {revealed.content ? (
              <div className="detail-section">
                <h4>粘贴原文</h4>
                <pre className="json-block">{revealed.content}</pre>
              </div>
            ) : null}
            {!revealed.url && !revealed.content ? <p className="muted">该订阅没有已登记的来源。</p> : null}
          </>
        ) : null}
      </Drawer>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="删除订阅"
        description={deleteTarget ? `将删除订阅 ${deleteTarget}，其导入的节点会在下次刷新后移除。` : undefined}
        confirmLabel="删除"
        danger
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget);
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
