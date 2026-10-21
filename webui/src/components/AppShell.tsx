import { useEffect, useState } from "react";
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
  Menu,
  Moon,
  Network,
  RefreshCw,
  Rss,
  Server,
  Settings,
  Stethoscope,
  Sun,
  Waypoints,
} from "lucide-react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { Button } from "./ui/Button";
import { cn } from "../lib/cn";
import { fetchSession, logout } from "../lib/api";
import { applyTheme, readTheme, type Theme } from "../lib/theme";

const navGroups = [
  {
    label: "仪表盘",
    items: [
      { label: "总览", path: "/overview", icon: LayoutDashboard },
      { label: "实时监控", path: "/monitor", icon: Activity },
    ],
  },
  {
    label: "资源管理",
    items: [
      { label: "模型管理", path: "/models", icon: Boxes },
      { label: "代理节点池", path: "/nodes", icon: Network },
      { label: "代理订阅", path: "/subscriptions", icon: Rss },
      { label: "模型轮转", path: "/rotation", icon: RefreshCw },
      { label: "上游管理", path: "/upstreams", icon: Server },
    ],
  },
  {
    label: "可观测",
    items: [
      { label: "请求日志", path: "/requests", icon: Logs },
      { label: "额度看板", path: "/quotas", icon: Gauge },
      { label: "路由诊断", path: "/diagnostics", icon: Stethoscope },
    ],
  },
  {
    label: "配置",
    items: [
      { label: "API 密钥", path: "/api-keys", icon: KeyRound },
      { label: "系统设置", path: "/config", icon: Settings },
    ],
  },
  {
    label: "高级",
    items: [
      { label: "接口调试", path: "/playground", icon: FlaskConical },
      { label: "接入手册", path: "/access", icon: BookOpen },
    ],
  },
];

export function AppShell() {
  const navigate = useNavigate();
  const location = useLocation();
  const session = useQuery({ queryKey: ["session"], queryFn: fetchSession, retry: false });
  const [theme, setTheme] = useState<Theme>(() => readTheme());
  const [navOpen, setNavOpen] = useState(false);

  useEffect(() => {
    const onUnauthorized = () => {
      navigate("/login", { replace: true });
    };
    window.addEventListener("mesh:unauthorized", onUnauthorized);
    return () => window.removeEventListener("mesh:unauthorized", onUnauthorized);
  }, [navigate]);

  useEffect(() => {
    setNavOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!navOpen) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setNavOpen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [navOpen]);

  const signOut = async () => {
    try {
      await logout();
    } finally {
      navigate("/login", { replace: true });
    }
  };

  const toggleTheme = () => {
    const next: Theme = theme === "dark" ? "light" : "dark";
    applyTheme(next);
    setTheme(next);
  };

  return (
    <div className="app-layout">
      <a className="skip-link" href="#main-content">
        跳到主内容
      </a>
      {navOpen ? (
        <button className="sidebar-scrim" type="button" aria-label="关闭导航" onClick={() => setNavOpen(false)} />
      ) : null}
      <aside className={cn("sidebar", navOpen && "sidebar-open")}>
        <div className="brand">
          <div className="brand-logo" aria-hidden="true">
            <Waypoints size={17} />
          </div>
          <div className="brand-copy">
            <div className="brand-title-row">
              <p className="brand-title">Mesh</p>
            </div>
            <p className="brand-subtitle">统一网关 · 出口调度</p>
          </div>
        </div>

        <div className="sidebar-main">
          <nav id="main-navigation" aria-label="主导航">
            {navGroups.map((group) => (
              <div className="nav-group" key={group.label}>
                <p className="nav-group-label">{group.label}</p>
                <div className="nav-list">
                  {group.items.map((item) => {
                    const Icon = item.icon;
                    return (
                      <NavLink
                        key={item.path}
                        to={item.path}
                        className={({ isActive }) => cn("nav-item", isActive && "nav-item-active")}
                      >
                        <Icon size={15} aria-hidden="true" />
                        <span>{item.label}</span>
                      </NavLink>
                    );
                  })}
                </div>
              </div>
            ))}
          </nav>
        </div>

        <div className="sidebar-bottom">
          <p className="sidebar-hint" title={session.data?.username}>
            {session.data ? `已登录：${session.data.username}` : "会话检查中"}
          </p>
          <div className="sidebar-tools">
            <Button
              variant="ghost"
              size="sm"
              className="sidebar-icon-btn"
              onClick={toggleTheme}
              aria-label={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}
              title={theme === "dark" ? "切换到浅色主题" : "切换到深色主题"}
            >
              {theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}
            </Button>
            <span className="sidebar-tool-spacer" />
            <Button
              variant="ghost"
              size="sm"
              className="sidebar-icon-btn"
              onClick={signOut}
              aria-label="退出登录"
              title="退出登录"
            >
              <LogOut size={15} />
            </Button>
          </div>
        </div>
      </aside>

      <main className="main" id="main-content" tabIndex={-1}>
        <div className="mobile-bar">
          <Button
            variant="ghost"
            size="sm"
            className="sidebar-icon-btn"
            onClick={() => setNavOpen((open) => !open)}
            aria-label="打开导航"
            aria-expanded={navOpen}
            aria-controls="main-navigation"
          >
            <Menu size={16} />
          </Button>
          <span className="mobile-bar-title">Mesh</span>
        </div>
        <div className="content">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
