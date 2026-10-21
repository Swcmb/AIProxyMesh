import React, { Component, type ReactNode } from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import "@fontsource/manrope/400.css";
import "@fontsource/manrope/500.css";
import "@fontsource/manrope/600.css";
import "@fontsource/manrope/700.css";
import "@fontsource/sora/600.css";
import "@fontsource/sora/700.css";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/mesh.css";
import "./styles/app.css";
import { AppRoutes } from "./app/routes";
import { ToastProvider } from "./components/ui/Toast";
import { initTheme } from "./lib/theme";

initTheme();

class AppErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="login-layout">
          <div className="login-card">
            <p className="eyebrow">UI Error</p>
            <h1 className="login-title">界面渲染失败</h1>
            <p className="muted pre-wrap">{this.state.error.message}</p>
            <button className="btn btn-primary btn-md" onClick={() => window.location.reload()}>
              重新加载
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 10_000,
    },
  },
});

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <AppErrorBoundary>
        <BrowserRouter>
          <ToastProvider>
            <AppRoutes />
          </ToastProvider>
        </BrowserRouter>
      </AppErrorBoundary>
    </QueryClientProvider>
  </React.StrictMode>,
);
