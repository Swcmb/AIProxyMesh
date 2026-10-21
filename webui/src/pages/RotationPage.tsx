import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowRight, RefreshCw } from "lucide-react";
import { api } from "../lib/api";
import type { RotationSnapshot } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function RotationPage() {
  const queryClient = useQueryClient();
  const snapshot = useQuery({ queryKey: ["rotation"], queryFn: () => api.get<RotationSnapshot>("/api/rotation") });

  const select = useMutation({
    mutationFn: ({ group, model }: { group: string; model: string }) =>
      api.post("/api/rotation/current", { group, model }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["rotation"] }),
  });

  if (snapshot.isLoading && !snapshot.data) {
    return <Loading label="读取轮转状态…" />;
  }
  if (snapshot.isError) {
    return <ErrorNotice message={formatApiErrorMessage(snapshot.error)} />;
  }
  const groups = snapshot.data?.groups ?? [];
  if (!groups.length) {
    return <EmptyState title="暂无轮转分组" />;
  }

  return (
    <>
      <PageHeader
        eyebrow="Rotation"
        title="模型轮转"
        description="两个别名按综合排名自动排序，按顺序故障切换；切换状态持久化并在重启后恢复。"
        actions={
          <>
            <Link className="muted" to="/models">
              查看模型排名 <ArrowRight size={13} />
            </Link>
            <Button variant="secondary" onClick={() => queryClient.invalidateQueries({ queryKey: ["rotation"] })}>
              <RefreshCw size={15} /> 刷新
            </Button>
          </>
        }
      />

      {snapshot.data?.persistence_error ? <ErrorNotice message={snapshot.data.persistence_error} /> : null}
      {select.isError ? <ErrorNotice message={formatApiErrorMessage(select.error)} /> : null}

      <div className="grid-2">
        {groups.map((group) => (
          <div className="card panel" key={group.id}>
            <div className="panel-head">
              <div>
                <h3>{group.alias}</h3>
                <p className="panel-sub">
                  当前：{group.current || "无可用模型"} · 首个输出超时 {snapshot.data?.first_output_seconds}s · 顺序由排名刷新
                </p>
              </div>
              <span className="badge badge-info">{group.id}</span>
            </div>
            {group.order.length === 0 ? (
              <EmptyState title="暂无候选模型" hint="等待模型目录探测到可路由的 Go 模型" />
            ) : (
              <div className="timeline">
                {group.order.map((model, index) => {
                  const paused = group.paused?.[model];
                  const available = group.available?.[model] !== false;
                  return (
                    <div className="timeline-item" key={model}>
                      <span className="muted">{index + 1}.</span> <span className="mono">{model}</span>
                      <div className="tag-list">
                        {model === group.current ? <span className="badge badge-success">当前</span> : null}
                        {paused ? <span className="badge badge-warning">暂停至 {formatDateTime(paused)}</span> : null}
                        {!available ? <span className="badge badge-danger">不可用</span> : null}
                      </div>
                      <div className="table-actions">
                        <Button
                          size="sm"
                          variant={model === group.current ? "secondary" : "primary"}
                          disabled={!available || model === group.current || select.isPending}
                          onClick={() => select.mutate({ group: group.id, model })}
                        >
                          {model === group.current ? "使用中" : "切换"}
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        ))}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>最近切换记录</h3>
          <span className="badge badge-neutral">{groups.reduce((sum, group) => sum + (group.switches?.length ?? 0), 0)} 条</span>
        </div>
        {groups.flatMap((group) => group.switches ?? []).length === 0 ? (
          <EmptyState title="尚无切换记录" />
        ) : (
          <div className="timeline">
            {groups
              .flatMap((group) => (group.switches ?? []).map((item) => ({ ...item, group: group.id })))
              .slice(-20)
              .reverse()
              .map((item, index) => (
                <div className="timeline-item" key={`${item.at}-${index}`}>
                  <span className="muted">{formatDateTime(item.at)}</span>
                  <span>
                    <span className="tag">{item.group}</span> <span className="mono">{item.from || "-"}</span> →{" "}
                    <span className="mono">{item.to || "-"}</span>
                  </span>
                  <span className="muted">{item.reason || "-"}</span>
                </div>
              ))}
          </div>
        )}
      </div>
    </>
  );
}
