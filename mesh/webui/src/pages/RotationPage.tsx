import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Save } from "lucide-react";
import { api } from "../lib/api";
import type { RotationGroup, RotationSnapshot } from "../lib/types";
import { EmptyState, ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { formatDateTime } from "../lib/time";
import { formatApiErrorMessage } from "../lib/error-message";

export function RotationPage() {
  const queryClient = useQueryClient();
  const snapshot = useQuery({ queryKey: ["rotation"], queryFn: () => api.get<RotationSnapshot>("/api/rotation") });
  const [draft, setDraft] = useState<Record<string, string[]>>({});

  const select = useMutation({
    mutationFn: ({ group, model }: { group: string; model: string }) =>
      api.post("/api/rotation/current", { group, model }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["rotation"] }),
  });
  const save = useMutation({
    mutationFn: () => {
      const groups = snapshot.data?.groups ?? [];
      const payload = {
        sota: { alias: groups.find((g) => g.id === "sota")?.alias ?? "sota", order: draft.sota ?? [] },
        sweet: { alias: groups.find((g) => g.id === "sweet")?.alias ?? "sweet", order: draft.sweet ?? [] },
        first_output_seconds: snapshot.data?.first_output_seconds ?? 30,
      };
      return api.put("/api/rotation", payload);
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["rotation"] }),
  });

  const groups = useMemo(() => {
    return (snapshot.data?.groups ?? []).map((group) => ({
      ...group,
      order: draft[group.id] ?? group.order ?? [],
    }));
  }, [snapshot.data, draft]);

  if (snapshot.isLoading && !snapshot.data) {
    return <Loading label="读取轮转状态…" />;
  }
  if (snapshot.isError) {
    return <ErrorNotice message={formatApiErrorMessage(snapshot.error)} />;
  }
  if (!groups.length) {
    return <EmptyState title="暂无轮转分组" />;
  }

  const move = (group: RotationGroup, index: number, offset: number) => {
    const order = [...(draft[group.id] ?? group.order ?? [])];
    const target = index + offset;
    if (target < 0 || target >= order.length) return;
    [order[index], order[target]] = [order[target], order[index]];
    setDraft((current) => ({ ...current, [group.id]: order }));
  };

  return (
    <>
      <PageHeader
        eyebrow="Rotation"
        title="模型轮转"
        description="sota / sweet 别名按顺序故障切换；切换状态持久化并在重启后恢复。"
        actions={
          <>
            <Button variant="secondary" onClick={() => queryClient.invalidateQueries({ queryKey: ["rotation"] })}>
              刷新
            </Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending || !draft.sota && !draft.sweet}>
              <Save size={15} /> 保存顺序
            </Button>
          </>
        }
      />

      {snapshot.data?.persistence_error ? <ErrorNotice message={snapshot.data.persistence_error} /> : null}
      {save.isError || select.isError ? <ErrorNotice message={formatApiErrorMessage(save.error ?? select.error)} /> : null}

      <div className="grid-2">
        {groups.map((group) => (
          <div className="card panel" key={group.id}>
            <div className="panel-head">
              <div>
                <h3>{group.alias}</h3>
                <p className="panel-sub">当前：{group.current || "未选择"} · 首个输出超时 {snapshot.data?.first_output_seconds}s</p>
              </div>
              <span className="badge badge-info">{group.id}</span>
            </div>
            <div className="timeline">
              {group.order.map((model, index) => {
                const paused = group.paused?.[model];
                const available = group.available?.[model] !== false;
                return (
                  <div className="timeline-item" key={model}>
                    <span className="mono">{model}</span>
                    <div className="tag-list">
                      {model === group.current ? <span className="badge badge-success">当前</span> : null}
                      {paused ? <span className="badge badge-warning">暂停至 {formatDateTime(paused)}</span> : null}
                      {!available ? <span className="badge badge-danger">不可用</span> : null}
                    </div>
                    <div className="table-actions">
                      <Button size="sm" variant="ghost" onClick={() => move(group, index, -1)} aria-label="上移">
                        <ArrowUp size={14} />
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => move(group, index, 1)} aria-label="下移">
                        <ArrowDown size={14} />
                      </Button>
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
          </div>
        ))}
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>最近切换记录</h3>
          <span className="badge badge-neutral">{snapshot.data?.groups.reduce((sum, group) => sum + (group.switches?.length ?? 0), 0) ?? 0} 条</span>
        </div>
        {(snapshot.data?.groups.flatMap((group) => (group.switches ?? []).map((item) => ({ ...item, group: group.id }))) ?? []).length === 0 ? (
          <EmptyState title="尚无切换记录" />
        ) : (
          <div className="timeline">
            {snapshot.data?.groups
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
