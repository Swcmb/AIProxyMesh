import type { CSSProperties } from "react";
import { cn } from "../../lib/cn";

type SkeletonProps = {
  width?: CSSProperties["width"];
  height?: CSSProperties["height"];
  className?: string;
};

export function Skeleton({ width = "100%", height = 12, className }: SkeletonProps) {
  return <span className={cn("skeleton", className)} style={{ width, height }} aria-hidden="true" />;
}
