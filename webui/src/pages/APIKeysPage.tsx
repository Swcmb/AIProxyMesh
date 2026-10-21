import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { APIKeyView } from "../lib/types";
import { ErrorNotice, Loading, PageHeader, StatCard } from "../components/Common";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { DataTable, type DataColumn } from "../components/ui/DataTable";
import { Modal } from "../components/ui/Modal";
import { Switch } from "../components/ui/Switch";
import { useToast } from "../components/ui/Toast";
import { formatDateTime, formatNumber } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function APIKeysPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [renameTarget, setRenameTarget] = useState<APIKeyView | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<APIKeyView | null>(null);

  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => api.get<{ keys: APIKeyView[] }>("/api/api-keys"),
  });
  const create = useMutation({
    mutationFn: () => api.post("/api/api-keys", { name: name.trim(), value: value.trim() }),
    onSuccess: () => {
      setName("");
      setValue("");
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      toast.success("API Key 已创建");
    },
  });
  const update = useMutation({
    mutationFn: ({ id, enabled, rename }: { id: string; enabled?: boolean; rename?: string }) =>
      api.patch(`/api/api-keys/${id}`, {
        ...(enabled === undefined ? {} : { enabled }),
        ...(rename === undefined ? {} : { name: rename }),
      }),
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      if (variables.rename !== undefined) {
        setRenameTarget(null);
        toast.success(`已改名为 ${variables.rename}`);
      }
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/api-keys/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      setDeleteTarget(null);
      toast.success("API Key 已删除");
    },
  });

  if (keys.isLoading && !keys.data) {
    return <Loading label="读取 API Key…" />;
  }
  if (keys.isError) {
    return <ErrorNotice message={formatApiErrorMessage(keys.error)} />;
  }
  const items = keys.data?.keys ?? [];
  const totalRequests = items.reduce((sum, item) => sum + item.usage.lifetime.requests, 0);
  const totalTokens = items.reduce((sum, item) => sum + item.usage.lifetime.total_tokens, 0);

  const columns: DataColumn<APIKeyView>[] = [
    { key: "name", header: "名称", cell: (item) => item.name },
    { key: "display", header: "Key（掩码）", cell: (item) => <span className="mono">{item.display}</span> },
    {
      key: "enabled",
      header: "启用",
      cell: (item) => (
        <Switch
          checked={item.enabled}
          disabled={update.isPending}
          onChange={(event) => update.mutate({ id: item.id, enabled: event.target.checked })}
          aria-label={`切换 ${item.name}`}
        />
      ),
    },
    { key: "created", header: "创建时间", cell: (item) => (item.created_at ? formatDateTime(item.created_at) : "-") },
    {
      key: "requests",
      header: "累计请求数",
      align: "right",
      cell: (item) => formatNumber(item.usage.lifetime.requests),
    },
    {
      key: "tokens",
      header: "累计 Token 数",
      align: "right",
      cell: (item) => formatNumber(item.usage.lifetime.total_tokens),
    },
    {
      key: "hour",
      header: "近 1 小时请求数",
      align: "right",
      cell: (item) => formatNumber(item.usage.last_hour.requests),
    },
    {
      key: "actions",
      header: "操作",
      cell: (item) => (
        <div className="table-actions">
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              setRenameTarget(item);
              setRenameValue(item.name);
            }}
          >
            <KeyRound size={13} /> 改名
          </Button>
          <Button size="sm" variant="danger" disabled={remove.isPending} onClick={() => setDeleteTarget(item)}>
            <Trash2 size={13} /> 删除
          </Button>
        </div>
      ),
    },
  ];

  return (
    <>
      <PageHeader eyebrow="API Keys" title="API Key 管理" description="网关对客户端签发的本地 Key；启停立即生效。密钥默认以掩码展示。" />

      <div className="stats-grid">
        <StatCard label="Key 总数" value={items.length} hint={`启用 ${items.filter((item) => item.enabled).length} 个`} />
        <StatCard label="累计请求数" value={formatNumber(totalRequests)} hint="所有 Key 合计" />
        <StatCard label="累计 Token 数" value={formatNumber(totalTokens)} hint="所有 Key 合计" />
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>新建 Key</h3>
          <span className="badge badge-neutral">留空自动生成</span>
        </div>
        <form
          className="form-grid"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate();
          }}
        >
          <label className="field-group">
            <span className="field-label">名称</span>
            <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：笔记本" required />
          </label>
          <label className="field-group">
            <span className="field-label">自定义值（可选）</span>
            <Input value={value} onChange={(event) => setValue(event.target.value)} placeholder="16–256 位字母数字" />
          </label>
          <div className="field-inline" style={{ alignItems: "flex-end" }}>
            <Button type="submit" disabled={create.isPending}>
              <Plus size={15} /> {create.isPending ? "创建中…" : "创建"}
            </Button>
          </div>
        </form>
        {create.isError ? <ErrorNotice message={formatApiErrorMessage(create.error)} /> : null}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>已签发 Key</h3>
          <span className="badge badge-neutral">{items.length}</span>
        </div>
        <DataTable
          columns={columns}
          rows={items}
          rowKey={(item) => item.id}
          loading={keys.isLoading}
          emptyTitle="暂无 API Key"
          emptyHint="创建后客户端可使用该 Key 访问网关"
          caption="已签发 API Key"
          minWidth={920}
        />
        {update.isError || remove.isError ? (
          <ErrorNotice message={formatApiErrorMessage(update.error ?? remove.error)} />
        ) : null}
      </div>

      <Modal
        open={Boolean(renameTarget)}
        onClose={() => setRenameTarget(null)}
        title="重命名 API Key"
        description={renameTarget ? `当前名称：${renameTarget.name}` : undefined}
        size="sm"
        footer={
          <>
            <Button variant="secondary" onClick={() => setRenameTarget(null)} disabled={update.isPending}>
              取消
            </Button>
            <Button
              onClick={() => {
                if (renameTarget && renameValue.trim()) {
                  update.mutate({ id: renameTarget.id, rename: renameValue.trim() });
                }
              }}
              disabled={!renameValue.trim() || update.isPending}
            >
              {update.isPending ? "保存中…" : "保存"}
            </Button>
          </>
        }
      >
        <label className="field-group">
          <span className="field-label">新名称</span>
          <Input
            value={renameValue}
            onChange={(event) => setRenameValue(event.target.value)}
            autoFocus
            onKeyDown={(event) => {
              if (event.key === "Enter" && renameTarget && renameValue.trim()) {
                update.mutate({ id: renameTarget.id, rename: renameValue.trim() });
              }
            }}
          />
        </label>
      </Modal>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="删除 API Key"
        description={deleteTarget ? `删除 ${deleteTarget.name}（${deleteTarget.display}）后立即生效，使用该 Key 的客户端将无法访问。` : undefined}
        confirmLabel="删除"
        danger
        pending={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.id);
        }}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  );
}
