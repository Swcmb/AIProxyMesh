import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Pencil, Plus, RefreshCw, Save, Trash2, Wand2, X } from "lucide-react";
import { api } from "../lib/api";
import type { ConfigView, EgressSnapshot, EgressSubscription, SubscriptionPreset } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Switch } from "../components/ui/Switch";
import { Textarea } from "../components/ui/Textarea";
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
};

const emptyDraft: Draft = { id: "", name: "", kind: "url", url: "", content: "", enabled: true, refresh_seconds: 3600 };

function draftPayload(draft: Draft): Record<string, unknown> {
  const body: Record<string, unknown> = { enabled: draft.enabled, name: draft.name.trim() };
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
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<{ id: string; url: string; content: string } | null>(null);

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
    onSuccess: invalidate,
  });
  const update = useMutation({
    mutationFn: ({ id, body }: { id: string; body: Record<string, unknown> }) => api.patch(`/api/egress/subscriptions/${id}`, body),
    onSuccess: invalidate,
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/egress/subscriptions/${id}`),
    onSuccess: invalidate,
  });
  const refresh = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/subscriptions/${id}/refresh`),
    onSuccess: invalidate,
  });
  const cleanup = useMutation({
    mutationFn: (id: string) => api.post(`/api/egress/subscriptions/${id}/cleanup`),
    onSuccess: invalidate,
  });
  const reveal = useMutation({
    mutationFn: async (id: string) => api.get<{ subscription: { url?: string; content?: string } }>(`/api/egress/subscriptions/${id}`),
    onSuccess: (data, id) => setRevealed({ id, url: data.subscription?.url ?? "", content: data.subscription?.content ?? "" }),
  });

  useEffect(() => {
    if (editingId && update.isSuccess) {
      setEditingId(null);
      setDraft(emptyDraft);
    }
  }, [editingId, update.isSuccess]);

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
        <StatCard label="订阅源" value={stats.total} hint={`${stats.enabled} 个启用`} />
        <StatCard label="导入节点" value={stats.nodes} hint="按订阅重复计数" />
        <StatCard label="不支持协议" value={stats.unsupported} hint="可见但不入候选池" />
        <StatCard label="旧代理导入" value={config.data?.egress?.import_legacy_proxies ? "开" : "关"} hint="proxies / proxyfile" />
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
                    <div className="muted mono">{preset.id} · {preset.format}</div>
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
                        create.mutate({ id: preset.id, name: preset.name, url: preset.url, enabled: true, refresh_seconds: 3600 })
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
        <div className="form-grid-tight">
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
            <Input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="主订阅" />
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
              <Input value={draft.url} onChange={(event) => setDraft({ ...draft, url: event.target.value })} placeholder="https://example.com/sub" />
            </label>
          ) : (
            <label className="field-group">
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
            <Switch checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} aria-label="启用该订阅" />
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

      {revealed ? (
        <div className="card panel">
          <div className="panel-head">
            <h3>订阅原文：{revealed.id}</h3>
            <Button size="sm" variant="ghost" onClick={() => setRevealed(null)} aria-label="收起原文">
              <X size={14} />
            </Button>
          </div>
          {revealed.url ? <p className="mono pre-wrap">{revealed.url}</p> : null}
          {revealed.content ? <pre className="json-block">{revealed.content}</pre> : null}
          {!revealed.url && !revealed.content ? <p className="muted">该订阅没有已登记的来源。</p> : null}
        </div>
      ) : null}

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
                  <th>来源</th>
                  <th>格式</th>
                  <th>节点</th>
                  <th>新增 / 移除</th>
                  <th>最近拉取</th>
                  <th>下次拉取</th>
                  <th>错误</th>
                  <th>启用</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {subscriptions.map((sub: EgressSubscription) => {
                  const entry = configSubscriptions.find((item) => item.id === sub.id);
                  return (
                    <tr key={sub.id}>
                      <td>
                        <strong>{sub.name || sub.id}</strong>
                        <div className="muted mono">{sub.id}</div>
                      </td>
                      <td>
                        {sub.inline ? (
                          <span className="badge badge-neutral">粘贴 {entry?.content_lines ?? 0} 行</span>
                        ) : (
                          <span className="mono">{sub.url ?? "-"}</span>
                        )}
                      </td>
                      <td>{sub.format || "-"}</td>
                      <td>
                        {sub.nodes}
                        <div className="muted">
                          支持 {sub.supported} · 不支持 {sub.unsupported}
                        </div>
                      </td>
                      <td>
                        {sub.added} / {sub.removed}
                      </td>
                      <td>{sub.last_fetch_at ? formatDateTime(sub.last_fetch_at) : "-"}</td>
                      <td>{sub.next_fetch_at ? formatDateTime(sub.next_fetch_at) : "手动"}</td>
                      <td className="pre-wrap">{sub.last_error || "-"}</td>
                      <td>
                        <Switch
                          checked={sub.enabled}
                          disabled={!entry || update.isPending}
                          onChange={(event) => update.mutate({ id: sub.id, body: { enabled: event.target.checked } })}
                          aria-label={`切换 ${sub.name || sub.id}`}
                        />
                      </td>
                      <td>
                        <div className="table-actions">
                          {entry ? (
                            <Button size="sm" variant="secondary" onClick={() => startEdit(sub.id)}>
                              <Pencil size={13} /> 编辑
                            </Button>
                          ) : null}
                          <Button size="sm" variant="secondary" disabled={reveal.isPending} onClick={() => reveal.mutate(sub.id)}>
                            <Eye size={13} /> 原文
                          </Button>
                          <Button size="sm" variant="secondary" disabled={refresh.isPending} onClick={() => refresh.mutate(sub.id)}>
                            <RefreshCw size={13} /> 刷新
                          </Button>
                          <Button size="sm" variant="secondary" disabled={busy} onClick={() => cleanup.mutate(sub.id)}>
                            <Wand2 size={13} /> 清理冷却
                          </Button>
                          {entry ? (
                            <Button size="sm" variant="danger" disabled={busy} onClick={() => remove.mutate(sub.id)}>
                              <Trash2 size={13} /> 删除
                            </Button>
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}
