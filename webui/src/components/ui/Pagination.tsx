import { Button } from "./Button";

type PaginationProps = {
  page?: number;
  pageSize?: number;
  total?: number;
  hasPrev?: boolean;
  hasNext?: boolean;
  onPrev?: () => void;
  onNext?: () => void;
  onPageChange?: (page: number) => void;
  label?: string;
};

export function Pagination({
  page,
  pageSize,
  total,
  hasPrev,
  hasNext,
  onPrev,
  onNext,
  onPageChange,
  label,
}: PaginationProps) {
  const pageCount = total !== undefined && pageSize ? Math.max(1, Math.ceil(total / pageSize)) : undefined;
  const prevDisabled = hasPrev !== undefined ? !hasPrev : page !== undefined ? page <= 1 : true;
  const nextDisabled =
    hasNext !== undefined ? !hasNext : page !== undefined && pageCount !== undefined ? page >= pageCount : true;

  const goPrev = () => {
    if (onPrev) {
      onPrev();
    } else if (onPageChange && page !== undefined) {
      onPageChange(page - 1);
    }
  };
  const goNext = () => {
    if (onNext) {
      onNext();
    } else if (onPageChange && page !== undefined) {
      onPageChange(page + 1);
    }
  };

  const info =
    label ??
    [total !== undefined ? `共 ${total} 条` : "", page !== undefined && pageCount !== undefined ? `第 ${page} / ${pageCount} 页` : ""]
      .filter(Boolean)
      .join(" · ");

  return (
    <div className="pagination">
      <span className="pagination-info">{info}</span>
      <div className="pagination-actions">
        <Button variant="secondary" size="sm" onClick={goPrev} disabled={prevDisabled}>
          上一页
        </Button>
        <Button variant="secondary" size="sm" onClick={goNext} disabled={nextDisabled}>
          下一页
        </Button>
      </div>
    </div>
  );
}
