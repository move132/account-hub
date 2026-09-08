import { lazy, Suspense } from "react";
import { Navigate, Outlet, Route, Routes } from "react-router-dom";
import { Spinner } from "../components/ui/spinner";
import { useAuth } from "./auth-context";
const AppLayout = lazy(() => import("./layout").then((module) => ({ default: module.AppLayout })));
const LoginPage = lazy(() => import("../features/auth/login-page").then((module) => ({ default: module.LoginPage })));
const PasswordPage = lazy(() => import("../features/auth/password-page").then((module) => ({ default: module.PasswordPage })));
const TokensPage = lazy(() => import("../features/tokens/tokens-page").then((module) => ({ default: module.TokensPage })));
const TokenDetailPage = lazy(() => import("../features/tokens/token-detail-page").then((module) => ({ default: module.TokenDetailPage })));
const DreaminaPage = lazy(() => import("../features/dreamina/dreamina-page").then((module) => ({ default: module.DreaminaPage })));
const DreaminaDetailPage = lazy(() => import("../features/dreamina/dreamina-detail-page").then((module) => ({ default: module.DreaminaDetailPage })));
const JobsPage = lazy(() => import("../features/jobs/jobs-page").then((module) => ({ default: module.JobsPage })));
const SettingsPage = lazy(() => import("../features/settings/settings-page").then((module) => ({ default: module.SettingsPage })));
const AuditPage = lazy(() => import("../features/audit/audit-page").then((module) => ({ default: module.AuditPage })));
function RequireAuth() {
  const { session, loading } = useAuth();
  if (loading)
    return <div className="grid min-h-screen place-items-center">
      <Spinner />
    </div>;
  return session ? <Outlet /> : <Navigate to="/login" replace />;
}
export function App() {
  return <Suspense fallback={<div className="grid min-h-screen place-items-center">
    <Spinner />
  </div>}>
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireAuth />}>
        <Route element={<AppLayout />}>
          <Route index element={<Navigate to="/tokens" replace />} />
          <Route path="/tokens" element={<TokensPage />} />
          <Route path="/tokens/:id" element={<TokenDetailPage />} />
          <Route path="/dreamina" element={<DreaminaPage />} />
          <Route path="/dreamina/:id" element={<DreaminaDetailPage />} />
          <Route path="/jobs" element={<JobsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/audit-logs" element={<AuditPage />} />
          <Route path="/account/password" element={<PasswordPage />} />
        </Route>
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  </Suspense>;
}
