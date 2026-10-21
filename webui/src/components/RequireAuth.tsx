import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Navigate } from "react-router-dom";
import { fetchSession } from "../lib/api";
import { Loading } from "./Common";

export function RequireAuth({ children }: { children: ReactNode }) {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: fetchSession,
    retry: false,
    staleTime: 60_000,
  });
  if (session.isLoading) {
    return (
      <div className="boot-screen">
        <Loading label="校验管理会话…" />
      </div>
    );
  }
  if (session.isError) {
    return <Navigate to="/login" replace />;
  }
  return children;
}
