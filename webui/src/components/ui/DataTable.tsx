import type { ReactNode } from "react";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { cn } from "../../lib/cn";
import { EmptyState, ErrorNotice } from "../Common";
import { Skeleton } from "./Skeleton";

export type DataColumn<T> = {
  key: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  align?: "left" | "right" | "center";
  sortKey?: string;
  width?: string;
  className?: string;
};

export type SortState = {
  key: string;
  order: "asc" | "desc";
};

type DataTableProps<T> = {
  columns: DataColumn<T>[];
  rows: T[] | undefined;
  rowKey: (row: T) => string;
  loading?: boolean;
  error?: string | null;
  emptyTitle?: string;
  emptyHint?: string;
  sort?: SortState | null;
  onSort?: (key: string) => void;
  onRowClick?: (row: T) => void;
  footer?: ReactNode;
  caption?: string;
  minWidth?: number;
  className?: string;
  skeletonRows?: number;
  maxHeight?: number;
};

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading,
  error,
  emptyTitle,
  emptyHint,
  sort,
  onSort,
  onRowClick,
  footer,
  caption,
  minWidth = 0,
  className,
  skeletonRows = 6,
  maxHeight,
}: DataTableProps<T>) {
  if (error) {
    return <ErrorNotice message={error} />;
  }
  const showSkeleton = loading && !rows;
  const rowList = rows ?? [];

  return (
    <div className={cn("dt-wrap", className)}>
      <div
        className="dt-scroll"
        style={maxHeight ? { maxHeight, overflowY: "auto" } : undefined}
      >
        <table className="dt" style={minWidth ? { minWidth } : undefined}>
          {caption ? <caption className="sr-only">{caption}</caption> : null}
          <thead>
            <tr>
              {columns.map((column) => {
                const active = Boolean(sort && column.sortKey && sort.key === column.sortKey);
                return (
                  <th
                    key={column.key}
                    className={cn(column.align && `dt-align-${column.align}`, column.sortKey && "dt-sortable")}
                    style={column.width ? { width: column.width } : undefined}
                    aria-sort={active && sort ? (sort.order === "asc" ? "ascending" : "descending") : undefined}
                  >
                    {column.sortKey && onSort ? (
                      <button
                        type="button"
                        className="dt-sort-btn"
                        onClick={() => onSort(column.sortKey!)}
                      >
                        <span>{column.header}</span>
                        {active && sort ? (
                          sort.order === "asc" ? (
                            <ArrowUp size={12} aria-hidden="true" />
                          ) : (
                            <ArrowDown size={12} aria-hidden="true" />
                          )
                        ) : (
                          <ChevronsUpDown size={12} className="dt-sort-idle" aria-hidden="true" />
                        )}
                      </button>
                    ) : (
                      column.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {showSkeleton
              ? Array.from({ length: skeletonRows }).map((_, index) => (
                  <tr key={`skeleton-${index}`}>
                    {columns.map((column) => (
                      <td key={column.key} className={cn(column.align && `dt-align-${column.align}`)}>
                        <Skeleton width="72%" />
                      </td>
                    ))}
                  </tr>
                ))
              : rowList.map((row) => (
                  <tr
                    key={rowKey(row)}
                    className={cn(onRowClick && "dt-row-clickable")}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                    tabIndex={onRowClick ? 0 : undefined}
                    onKeyDown={
                      onRowClick
                        ? (event) => {
                            if (event.key === "Enter" || event.key === " ") {
                              event.preventDefault();
                              onRowClick(row);
                            }
                          }
                        : undefined
                    }
                  >
                    {columns.map((column) => (
                      <td key={column.key} className={cn(column.align && `dt-align-${column.align}`, column.className)}>
                        {column.cell(row)}
                      </td>
                    ))}
                  </tr>
                ))}
          </tbody>
        </table>
      </div>
      {!showSkeleton && rowList.length === 0 ? <EmptyState title={emptyTitle} hint={emptyHint} /> : null}
      {footer ? <div className="dt-footer">{footer}</div> : null}
    </div>
  );
}
