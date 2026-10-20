import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { APIKeyView } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { Switch } from "../components/ui/Switch";
import { formatDateTime, formatNumber } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function APIKeysPage() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
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
    },
  });
  const update = useMutation({
    mutationFn: ({ id, enabled, rename }: { id: string; enabled?: boolean; rename?: string }) =>
      api.patch(`/api/api-keys/${id}`, {
        ...(enabled === undefined ? {} : { enabled }),
        ...(rename === undefined ? {} : { name: rename }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
  });
  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/api/api-keys/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
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

  return (
    <>
      <PageHeader eyebrow="API Keys" title="API Key 管理" description="网关对客户端签发的本地 Key；启停立即生效。" />

      <div className="stats-grid">
        <div className="stat-card">
          <span className="stat-label">Key 总数</span>
          <p className="stat-value">{items.length}</p>
          <span className="stat-hint">启用 {items.filter((item) => item.enabled).length}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">累计请求</span>
          <p className="stat-value">{formatNumber(totalRequests)}</p>
        </div>
        <div className="stat-card">
          <span className="stat-label">累计 Token</span>
          <p className="stat-value">{formatNumber(totalTokens)}</p>
        </div>
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>新建 Key</h3>
          <span className="badge badge-neutral">留空自动生成</span>
        </div>
        <form
          className="form-grid-tight"
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
        {items.length === 0 ? (
          <EmptyState title="暂无 API Key" />
        ) : (
          <div className="data-table-wrap scroll-x">
            <table className="data-table">
              <thead>
                <tr>
                  <th>名称</th>
                  <th>Key</th>
                  <th>启用</th>
                  <th>创建时间</th>
                  <th>累计请求</th>
                  <th>累计 Token</th>
                  <th>近 1 小时</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id}>
                    <td>{item.name}</td>
                    <td className="mono">{item.display}</td>
                    <td>
                      <Switch
                        checked={item.enabled}
                        onChange={(event) => update.mutate({ id: item.id, enabled: event.target.checked })}
                        aria-label={`切换 ${item.name}`}
                      />
                    </td>
                    <td>{item.created_at ? formatDateTime(item.created_at) : "-"}</td>
                    <td>{formatNumber(item.usage.lifetime.requests)}</td>
                    <td>{formatNumber(item.usage.lifetime.total_tokens)}</td>
                    <td>{formatNumber(item.usage.last_hour.requests)}</td>
                    <td>
                      <div className="table-actions">
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => {
                            const next = window.prompt("新的名称", item.name);
                            if (next && next.trim()) {
                              update.mutate({ id: item.id, rename: next.trim() });
                            }
                          }}
                        >
                          <KeyRound size={13} /> 改名
                        </Button>
                        <Button
                          size="sm"
                          variant="danger"
                          disabled={remove.isPending}
                          onClick={() => {
                            if (window.confirm(`确认删除 ${item.name}？该操作立即生效。`)) {
                              remove.mutate(item.id);
                            }
                          }}
                        >
                          <Trash2 size={13} /> 删除
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {update.isError || remove.isError ? (
          <ErrorNotice message={formatApiErrorMessage(update.error ?? remove.error)} />
        ) : null}
      </div>
    </>
  );
}
