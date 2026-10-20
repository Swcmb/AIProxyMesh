import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "../components/AppShell";
import { LoginPage } from "../components/LoginPage";
import { RequireAuth } from "../components/RequireAuth";
import { OverviewPage } from "../pages/OverviewPage";
import { ModelsPage } from "../pages/ModelsPage";
import { UpstreamsPage } from "../pages/UpstreamsPage";
import { RotationPage } from "../pages/RotationPage";
import { RankingPage } from "../pages/RankingPage";
import { APIKeysPage } from "../pages/APIKeysPage";
import { QuotasPage } from "../pages/QuotasPage";
import { SubscriptionsPage } from "../pages/SubscriptionsPage";
import { NodesPage } from "../pages/NodesPage";
import { RequestsPage } from "../pages/RequestsPage";
import { PlaygroundPage } from "../pages/PlaygroundPage";
import { DiagnosticsPage } from "../pages/DiagnosticsPage";
import { ConfigPage } from "../pages/ConfigPage";
import { MonitorPage } from "../pages/MonitorPage";

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route path="/" element={<Navigate to="/overview" replace />} />
        <Route path="/overview" element={<OverviewPage />} />
        <Route path="/models" element={<ModelsPage />} />
        <Route path="/upstreams" element={<UpstreamsPage />} />
        <Route path="/rotation" element={<RotationPage />} />
        <Route path="/rankings" element={<RankingPage />} />
        <Route path="/api-keys" element={<APIKeysPage />} />
        <Route path="/quotas" element={<QuotasPage />} />
        <Route path="/subscriptions" element={<SubscriptionsPage />} />
        <Route path="/nodes" element={<NodesPage />} />
        <Route path="/requests" element={<RequestsPage />} />
        <Route path="/playground" element={<PlaygroundPage />} />
        <Route path="/diagnostics" element={<DiagnosticsPage />} />
        <Route path="/config" element={<ConfigPage />} />
        <Route path="/monitor" element={<MonitorPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/overview" replace />} />
    </Routes>
  );
}
