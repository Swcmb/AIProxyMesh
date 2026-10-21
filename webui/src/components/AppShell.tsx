import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  BookOpen,
  Boxes,
  FlaskConical,
  Gauge,
  KeyRound,
  LayoutDashboard,
  Logs,
  LogOut,
  Network,
  RefreshCw,
  Rss,
  Settings,
  Stethoscope,
  Waypoints,
} from "lucide-react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { Button } from "./ui/Button";
import { cn } from "../lib/cn";
import { fetchSession, logout } from "../lib/api";

const navItems = [
  { label: "总览", path: "/overview", icon: LayoutDashboard },
  { label: "模型管理", path: "/models", icon: Boxes },
  { label: "接入手册", path: "/access", icon: BookOpen },
  { label: "上游管理", path: "/upstreams", icon: KeyRound },
  { label: "模型轮转", path: "/rotation", icon: RefreshCw },
  { label: "API Key", path: "/api-keys", icon: KeyRound },
  { label: "额度看板", path: "/quotas", icon: Gauge },
  { label: "代理订阅", path: "/subscriptions", icon: Rss },
  { label: "代理节点池", path: "/nodes", icon: Network },
  { label: "请求与切换日志", path: "/requests", icon: Logs },
  { label: "Playground", path: "/playground", icon: FlaskConical },
  { label: "路由诊断", path: "/diagnostics", icon: Stethoscope },
  { label: "配置管理", path: "/config", icon: Settings },
  { label: "实时监控", path: "/monitor", icon: Activity },
];

export function AppShell() {
  const navigate = useNavigate();
  const session = useQuery({ queryKey: ["session"], queryFn: fetchSession, retry: false });

  useEffect(() => {
    const onUnauthorized = () => {
      navigate("/login", { replace: true });
    };
    window.addEventListener("mesh:unauthorized", onUnauthorized);
    return () => window.removeEventListener("mesh:unauthorized", onUnauthorized);
  }, [navigate]);

  const signOut = async () => {
    try {
      await logout();
    } finally {
      navigate("/login", { replace: true });
    }
  };

  return (
    <div className="app-layout">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-logo" aria-hidden="true">
            <Waypoints size={19} color="#1470ff" />
          </div>
          <div className="brand-copy">
            <div className="brand-title-row">
              <p className="brand-title">Mesh</p>
            </div>
            <p className="brand-subtitle">统一网关 · 出口调度</p>
          </div>
        </div>

        <div className="sidebar-main">
          <nav className="nav-list" aria-label="主导航">
            {navItems.map((item) => {
              const Icon = item.icon;
              return (
                <NavLink
                  key={item.path}
                  to={item.path}
                  className={({ isActive }) => cn("nav-item", isActive && "nav-item-active")}
                >
                  <Icon size={16} />
                  <span>{item.label}</span>
                </NavLink>
              );
            })}
          </nav>
        </div>

        <div className="sidebar-bottom">
          <p className="sidebar-hint">{session.data ? `已登录：${session.data.username}` : "会话检查中"}</p>
          <div className="sidebar-tools">
            <Button variant="secondary" size="sm" className="sidebar-icon-btn" onClick={signOut} aria-label="退出登录" title="退出登录">
              <LogOut size={16} />
            </Button>
          </div>
        </div>
      </aside>
      <main className="main">
        <div className="content">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
