import { useState } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LockKeyhole, Waypoints } from "lucide-react";
import { Button } from "./ui/Button";
import { Input } from "./ui/Input";
import { fetchSession, login } from "../lib/api";
import { formatApiErrorMessage } from "../lib/error-message";

export function LoginPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const session = useQuery({ queryKey: ["session"], queryFn: fetchSession, retry: false });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const mutation = useMutation({
    mutationFn: () => login(username.trim(), password),
    onSuccess: (data) => {
      queryClient.setQueryData(["session"], data);
      navigate("/overview", { replace: true });
    },
  });

  if (session.isSuccess) {
    return <Navigate to="/overview" replace />;
  }

  return (
    <div className="login-layout">
      <div className="login-card">
        <div className="login-header">
          <div className="brand-logo" aria-hidden="true">
            <Waypoints size={20} color="#1470ff" />
          </div>
          <div className="login-heading-copy">
            <p className="eyebrow">Mesh Console</p>
            <h1 className="login-title">登录统一网关</h1>
            <p className="muted">使用配置中的 WebUI 账号访问管理面。</p>
          </div>
        </div>
        <form
          className="login-form"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.mutate();
          }}
        >
          <label className="field-group">
            <span className="field-label">用户名</span>
            <Input
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              placeholder="admin"
              required
            />
          </label>
          <label className="field-group">
            <span className="field-label">密码</span>
            <Input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              required
            />
          </label>
          {mutation.isError ? <p className="field-error">{formatApiErrorMessage(mutation.error)}</p> : null}
          <Button type="submit" className="login-submit" disabled={mutation.isPending}>
            <LockKeyhole size={16} />
            {mutation.isPending ? "登录中…" : "登录"}
          </Button>
        </form>
      </div>
    </div>
  );
}
