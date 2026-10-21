import type { ReactNode } from "react";
import { Search } from "lucide-react";
import { cn } from "../../lib/cn";

type FilterBarProps = {
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
};

export function FilterBar({ children, actions, className }: FilterBarProps) {
  return (
    <div className={cn("filter-bar", className)}>
      {children}
      {actions ? <div className="filter-actions">{actions}</div> : null}
    </div>
  );
}

type SearchFieldProps = {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  ariaLabel?: string;
  className?: string;
};

export function SearchField({
  value,
  onChange,
  placeholder = "搜索…",
  ariaLabel = "搜索",
  className,
}: SearchFieldProps) {
  return (
    <div className={cn("search-field", className)}>
      <Search size={14} aria-hidden="true" />
      <input
        className="search-input"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        aria-label={ariaLabel}
        type="search"
      />
    </div>
  );
}
