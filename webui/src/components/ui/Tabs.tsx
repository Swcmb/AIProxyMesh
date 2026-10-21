import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export type TabItem = {
  key: string;
  label: ReactNode;
};

type TabsProps = {
  items: TabItem[];
  value: string;
  onChange: (key: string) => void;
  ariaLabel: string;
  className?: string;
};

export function Tabs({ items, value, onChange, ariaLabel, className }: TabsProps) {
  const move = (delta: number) => {
    const index = items.findIndex((item) => item.key === value);
    const next = items[(index + delta + items.length) % items.length];
    if (next) {
      onChange(next.key);
    }
  };

  return (
    <div className={cn("tabs", className)} role="tablist" aria-label={ariaLabel}>
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          role="tab"
          aria-selected={value === item.key}
          tabIndex={value === item.key ? 0 : -1}
          className={cn("tab", value === item.key && "tab-active")}
          onClick={() => onChange(item.key)}
          onKeyDown={(event) => {
            if (event.key === "ArrowRight") {
              event.preventDefault();
              move(1);
            } else if (event.key === "ArrowLeft") {
              event.preventDefault();
              move(-1);
            }
          }}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
