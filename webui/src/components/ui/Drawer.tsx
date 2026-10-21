import { useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { Button } from "./Button";
import { cn } from "../../lib/cn";
import { useBodyScrollLock, useEscape, useFocusTrap } from "../../lib/use-focus-trap";

type DrawerProps = {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  size?: "md" | "lg";
};

export function Drawer({ open, onClose, title, subtitle, actions, children, size = "md" }: DrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  useFocusTrap(open, panelRef);
  useEscape(open, onClose);
  useBodyScrollLock(open);

  if (!open) {
    return null;
  }

  return createPortal(
    <div
      className="drawer-overlay"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div
        ref={panelRef}
        className={cn("drawer-panel", `drawer-${size}`)}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
      >
        <header className="drawer-header">
          <div>
            <h3>{title}</h3>
            {subtitle ? <p>{subtitle}</p> : null}
          </div>
          <div className="drawer-header-actions">
            {actions}
            <Button variant="ghost" size="sm" className="modal-close" onClick={onClose} aria-label="关闭">
              <X size={15} />
            </Button>
          </div>
        </header>
        <div className="drawer-body">{children}</div>
      </div>
    </div>,
    document.body,
  );
}
