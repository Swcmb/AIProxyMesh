import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { CheckCircle2, Info, X, XCircle } from "lucide-react";
import { cn } from "../../lib/cn";

type ToastKind = "success" | "error" | "info";

type ToastItem = {
  id: number;
  kind: ToastKind;
  message: string;
};

type ToastApi = {
  push: (kind: ToastKind, message: string) => void;
  success: (message: string) => void;
  error: (message: string) => void;
  info: (message: string) => void;
};

const ToastContext = createContext<ToastApi | null>(null);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(1);

  const remove = useCallback((id: number) => {
    setItems((list) => list.filter((item) => item.id !== id));
  }, []);

  const push = useCallback(
    (kind: ToastKind, message: string) => {
      const id = nextId.current++;
      setItems((list) => [...list.slice(-4), { id, kind, message }]);
      window.setTimeout(() => remove(id), 5000);
    },
    [remove],
  );

  const api = useMemo<ToastApi>(
    () => ({
      push,
      success: (message) => push("success", message),
      error: (message) => push("error", message),
      info: (message) => push("info", message),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toast-container" aria-live="polite">
        {items.map((item) => (
          <div
            key={item.id}
            className={cn("toast-item", `toast-${item.kind}`)}
            role={item.kind === "error" ? "alert" : "status"}
          >
            {item.kind === "success" ? (
              <CheckCircle2 size={15} aria-hidden="true" />
            ) : item.kind === "error" ? (
              <XCircle size={15} aria-hidden="true" />
            ) : (
              <Info size={15} aria-hidden="true" />
            )}
            <span className="toast-text">{item.message}</span>
            <button
              type="button"
              className="toast-close"
              onClick={() => remove(item.id)}
              aria-label="关闭通知"
            >
              <X size={13} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error("useToast 必须在 ToastProvider 内使用");
  }
  return context;
}
