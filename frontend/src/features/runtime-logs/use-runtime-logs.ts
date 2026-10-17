import { useCallback, useEffect, useRef, useState } from "react";
import { useAuth } from "../../app/auth-context";
import { api, ApiError } from "../../lib/api";
import { mergeLogBatch, parseLogBatch, type RuntimeLogView } from "./log-state";

export type LogConnection = "connecting" | "connected" | "reconnecting";

export function useRuntimeLogs() {
  const { invalidateSession } = useAuth();
  const buffer = useRef<RuntimeLogView>({ items: [], truncated: false });
  const cursor = useRef("");
  const [view, setView] = useState(buffer.current);
  const [connection, setConnection] = useState<LogConnection>("connecting");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let disposed = false;
    let source: EventSource;
    let publishTimer: number | undefined;
    let reconnectTimer: number | undefined;
    let checkingSession = false;
    const controller = new AbortController();

    const expireSession = () => {
      if (disposed) return;
      source.close();
      invalidateSession();
    };
    const checkSession = async () => {
      if (checkingSession) return;
      checkingSession = true;
      try {
        await api("/api/v1/auth/session", { signal: controller.signal });
      } catch (error) {
        if (!disposed && error instanceof ApiError && ["AUTH_REQUIRED", "SESSION_INVALID"].includes(error.errorCode ?? "")) {
          expireSession();
        }
      } finally {
        checkingSession = false;
      }
    };
    const scheduleReconnect = () => {
      if (reconnectTimer !== undefined || disposed) return;
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = undefined;
        connect();
      }, 3000);
    };
    const connect = () => {
      if (disposed) return;
      source?.close();
      const query = cursor.current ? "?after=" + encodeURIComponent(cursor.current) : "";
      const current = new EventSource("/api/v1/runtime-logs/stream" + query, { withCredentials: true });
      source = current;
      setConnection(cursor.current ? "reconnecting" : "connecting");
      current.onopen = () => {
        if (!disposed && source === current) setConnection("connected");
      };
      current.addEventListener("logs", (event: MessageEvent<string>) => {
        if (disposed || source !== current) return;
        try {
          buffer.current = mergeLogBatch(buffer.current, parseLogBatch(event.data));
          cursor.current = event.lastEventId;
          // Coalesce bursts so every individual backend write does not render.
          if (publishTimer === undefined) {
            publishTimer = window.setTimeout(() => {
              publishTimer = undefined;
              if (!disposed) setView(buffer.current);
            }, 100);
          }
        } catch {
          current.close();
          setConnection("reconnecting");
          scheduleReconnect();
        }
      });
      current.addEventListener("auth-expired", expireSession);
      current.onerror = () => {
        if (disposed || source !== current) return;
        setConnection("reconnecting");
        void checkSession();
        // EventSource retries network failures itself. HTTP errors close it.
        if (current.readyState === EventSource.CLOSED) scheduleReconnect();
      };
    };
    connect();
    return () => {
      disposed = true;
      controller.abort();
      source?.close();
      window.clearTimeout(publishTimer);
      window.clearTimeout(reconnectTimer);
    };
  }, [attempt, invalidateSession]);

  const clear = useCallback(() => {
    buffer.current = { items: [], truncated: false };
    setView(buffer.current);
  }, []);
  const reconnect = useCallback(() => setAttempt((value) => value + 1), []);
  return { ...view, connection, clear, reconnect };
}
