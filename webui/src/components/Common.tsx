import type { ReactNode } from "react";
import { AlertTriangle, Inbox, LoaderCircle } from "lucide-react";
import { cn } from "../lib/cn";

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="topbar">
      <div>
        {eyebrow ? <span className="eyebrow">{eyebrow}</span> : null}
        <h1>{title}</h1>
        {description ? <p className="muted">{description}</p> : null}
      </div>
      {actions ? <div className="button-row">{actions}</div> : null}
    </div>
  );
}

export function StatCard({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: ReactNode;
  hint?: string;
  tone?: "success" | "warning" | "danger";
}) {
  return (
    <div className="stat-card">
      <span className="stat-label">{label}</span>
      <p className={cn("stat-value", tone && `stat-${tone}`)}>{value}</p>
      {hint ? <span className="stat-hint">{hint}</span> : null}
    </div>
  );
}

export function Loading({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="state-panel">
      <LoaderCircle size={18} className="spin" />
      <span>{label}</span>
    </div>
  );
}

export function EmptyState({ title = "暂无数据", hint }: { title?: string; hint?: string }) {
  return (
    <div className="state-panel">
      <Inbox size={20} />
      <strong>{title}</strong>
      {hint ? <span className="muted">{hint}</span> : null}
    </div>
  );
}

export function ErrorNotice({ message }: { message: string }) {
  return (
    <div className="callout callout-error">
      <AlertTriangle size={16} />
      <span>{message}</span>
    </div>
  );
}

export function StatusBadge({
  ok,
  label,
  pendingLabel,
}: {
  ok: boolean | null | undefined;
  label?: string;
  pendingLabel?: string;
}) {
  if (ok === null || ok === undefined) {
    return <span className="badge badge-muted">{pendingLabel ?? "未知"}</span>;
  }
  return <span className={cn("badge", ok ? "badge-success" : "badge-danger")}>{label ?? (ok ? "正常" : "异常")}</span>;
}

export function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="json-block">{JSON.stringify(value, null, 2)}</pre>
  );
}
