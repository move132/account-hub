import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import * as Toast from "radix-ui/toast";
import CircleCheck from "lucide-react/dist/esm/icons/circle-check.mjs";
import CircleX from "lucide-react/dist/esm/icons/circle-x.mjs";
import Info from "lucide-react/dist/esm/icons/info.mjs";
import TriangleAlert from "lucide-react/dist/esm/icons/triangle-alert.mjs";
import X from "lucide-react/dist/esm/icons/x.mjs";
import { cn } from "./primitives";
import { sanitizeToastText } from "./toast-text";
export type ToastTone = "info" | "success" | "warning" | "danger";
interface Notice {
  id: number;
  title: string;
  description?: string;
  tone: ToastTone;
  open: boolean;
}
interface ToastValue {
  notify: (title: string, description?: string, tone?: ToastTone) => void;
}
const removeDelay = 220;
const toneStyles: Record<ToastTone, {
  root: string;
  icon: string;
  title: string;
}> = {
  info: {
    root: "border-app-info/35 border-l-app-info",
    icon: "bg-app-info/10 text-app-info",
    title: "text-app-info",
  },
  success: {
    root: "border-app-success/35 border-l-app-success",
    icon: "bg-app-success/10 text-app-success",
    title: "text-app-success",
  },
  warning: {
    root: "border-app-warning/35 border-l-app-warning",
    icon: "bg-app-warning/10 text-app-warning",
    title: "text-app-warning",
  },
  danger: {
    root: "border-app-danger/35 border-l-app-danger",
    icon: "bg-app-danger/10 text-app-danger",
    title: "text-app-danger",
  },
};
const ToastContext = createContext<ToastValue | null>(null);
export function ToastProvider({ children }: {
  children: ReactNode;
}) {
  const [notices, setNotices] = useState<Notice[]>([]);
  const nextId = useRef(0);
  const removeTimers = useRef(new Map<number, ReturnType<typeof setTimeout>>());
  useEffect(() => () => {
    removeTimers.current.forEach(clearTimeout);
    removeTimers.current.clear();
  }, []);
  const notify = useCallback((title: string, description?: string, tone: ToastTone = "success") => {
    nextId.current += 1;
    const notice = {
      id: nextId.current,
      title: sanitizeToastText(title) || "通知",
      description: sanitizeToastText(description),
      tone,
      open: true,
    };
    setNotices((current) => [notice, ...current]);
  }, []);
  const dismiss = useCallback((id: number) => {
    if (removeTimers.current.has(id))
      return;
    setNotices((current) => current.map((notice) => notice.id === id ? { ...notice, open: false } : notice));
    const timer = setTimeout(() => {
      setNotices((current) => current.filter((notice) => notice.id !== id));
      removeTimers.current.delete(id);
    }, removeDelay);
    removeTimers.current.set(id, timer);
  }, []);
  const value = useMemo(() => ({ notify }), [notify]);
  return <Toast.Provider label="通知" duration={3000} swipeDirection="right" swipeThreshold={48}>
    <ToastContext.Provider value={value}>
      {children}</ToastContext.Provider>
    {notices.map((notice) => {
      const styles = toneStyles[notice.tone];
      return <Toast.Root key={notice.id} open={notice.open} type={notice.tone === "danger" || notice.tone === "warning" ? "foreground" : "background"} onOpenChange={(open) => {
        if (!open)
          dismiss(notice.id);
      }} className={cn("toast-root pointer-events-auto relative grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 overflow-hidden rounded-lg border border-l-[3px] bg-app-surface p-3 shadow-xl", styles.root)}>
        <span className={cn("grid size-8 shrink-0 place-items-center rounded-md", styles.icon)}>
          <ToneIcon tone={notice.tone} />
        </span>
        <div className="min-w-0 pt-0.5">
          <Toast.Title className={cn("break-words text-sm font-semibold", styles.title)}>
            {notice.title}</Toast.Title>
          {notice.description && <Toast.Description className="mt-0.5 break-words text-xs leading-5 text-app-subtle">
            {notice.description}</Toast.Description>}
        </div>
        <Toast.Close aria-label="关闭通知" className="-mr-1 -mt-1 grid size-7 place-items-center rounded-md text-app-subtle outline-none transition-colors hover:bg-app-hover hover:text-app-foreground">
          <X aria-hidden="true" className="size-3.5" />
        </Toast.Close>
      </Toast.Root>;
    })}<Toast.Viewport className="pointer-events-none fixed right-3 top-3 z-[100] m-0 flex max-h-[calc(100vh-1.5rem)] w-[calc(100vw-1.5rem)] max-w-sm list-none flex-col gap-2 p-0 outline-none sm:right-5 sm:top-5 sm:max-h-[calc(100vh-2.5rem)]" />
  </Toast.Provider>;
}
function ToneIcon({ tone }: {
  tone: ToastTone;
}) {
  const className = "size-4";
  if (tone === "success")
    return <CircleCheck aria-hidden="true" className={className} />;
  if (tone === "warning")
    return <TriangleAlert aria-hidden="true" className={className} />;
  if (tone === "danger")
    return <CircleX aria-hidden="true" className={className} />;
  return <Info aria-hidden="true" className={className} />;
}
export function useToast() {
  const value = useContext(ToastContext);
  if (!value)
    throw new Error("useToast must be used inside ToastProvider");
  return value;
}
