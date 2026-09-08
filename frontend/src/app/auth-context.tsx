import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, jsonBody } from "../lib/api";
export interface Session {
  username: string;
  expires_at: number;
  password_managed_by_env: boolean;
}
interface AuthValue {
  session: Session | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  reload: () => Promise<void>;
}
const AuthContext = createContext<AuthValue | null>(null);
export function AuthProvider({ children }: {
  children: ReactNode;
}) {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const reload = useCallback(async () => {
    try {
      const data = await api<Session & Record<string, unknown>>("/api/v1/auth/session");
      setSession(data);
    }
    catch {
      setSession(null);
    }
    finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void reload(); }, [reload]);
  const login = useCallback(async (username: string, password: string) => {
    const data = await api<Session & Record<string, unknown>>("/api/v1/auth/login", { method: "POST", ...jsonBody({ username, password }) });
    setSession(data);
  }, []);
  const logout = useCallback(async () => {
    await api("/api/v1/auth/logout", { method: "POST", ...jsonBody({}) });
    setSession(null);
  }, []);
  const value = useMemo(() => ({ session, loading, login, logout, reload }), [session, loading, login, logout, reload]);
  return <AuthContext.Provider value={value}>
    {children}</AuthContext.Provider>;
}
export function useAuth() {
  const value = useContext(AuthContext);
  if (!value)
    throw new Error("useAuth must be used inside AuthProvider");
  return value;
}
