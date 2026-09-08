import { useState, type ReactNode } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import Key from "lucide-react/dist/esm/icons/key.mjs";
import ListTodo from "lucide-react/dist/esm/icons/list-todo.mjs";
import LockKeyhole from "lucide-react/dist/esm/icons/lock-keyhole.mjs";
import LogOut from "lucide-react/dist/esm/icons/log-out.mjs";
import Menu from "lucide-react/dist/esm/icons/menu.mjs";
import Moon from "lucide-react/dist/esm/icons/moon.mjs";
import ScrollText from "lucide-react/dist/esm/icons/scroll-text.mjs";
import Settings from "lucide-react/dist/esm/icons/settings.mjs";
import ShieldCheck from "lucide-react/dist/esm/icons/shield-check.mjs";
import Sun from "lucide-react/dist/esm/icons/sun.mjs";
import UsersRound from "lucide-react/dist/esm/icons/users-round.mjs";
import { ActionMenu, Button, ConfirmDialog, Dialog, cn, useToast } from "../components/ui";
import { useAuth } from "./auth-context";
import { useTheme, type ThemeMode } from "./theme-provider";
import { errorMessage } from "../lib/api";
import { PasswordForm } from "../features/auth/password-page";
const nav = [
  { to: "/tokens", label: "GPT账号", icon: <Key aria-hidden="true" className="size-4" /> },
  { to: "/dreamina", label: "即梦账号", icon: <UsersRound aria-hidden="true" className="size-4" /> },
  { to: "/jobs", label: "批量任务", icon: <ListTodo aria-hidden="true" className="size-4" /> },
  { to: "/settings", label: "系统设置", icon: <Settings aria-hidden="true" className="size-4" /> },
  { to: "/audit-logs", label: "审计日志", icon: <ScrollText aria-hidden="true" className="size-4" /> },
];
const themeIcons = {
  dark: <Sun aria-hidden="true" className="size-5" />,
  light: <Moon aria-hidden="true" className="size-5" />,
} satisfies Record<ThemeMode, ReactNode>;
export function AppLayout() {
  const { session, logout } = useAuth();
  const { mode, setMode } = useTheme();
  const { notify } = useToast();
  const navigate = useNavigate();
  const [menuOpen, setMenuOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [logoutConfirmOpen, setLogoutConfirmOpen] = useState(false);
  const toggleTheme = () => setMode(mode === "dark" ? "light" : "dark");
  const themeLabel = mode === "dark" ? "Switch to light theme" : "Switch to dark theme";
  const onLogout = async () => {
    try {
      await logout();
      navigate("/login");
    }
    catch (error) {
      notify("退出失败", errorMessage(error), "danger");
    }
  };
  const accountActions = [
    { label: "修改密码", icon: <LockKeyhole aria-hidden="true" className="size-3.5" />, onSelect: () => setPasswordOpen(true) },
    { label: "退出", icon: <LogOut aria-hidden="true" className="size-3.5" />, onSelect: () => setLogoutConfirmOpen(true), danger: true },
  ];
  return <div className="min-h-screen bg-app-background text-app-foreground">
    <a href="#main-content" className="fixed left-3 top-3 z-[120] -translate-y-20 rounded-lg bg-app-primary px-3 py-2 font-medium text-black focus:translate-y-0">跳到主内容</a>
    <aside className="fixed inset-y-0 left-0 z-20 hidden w-56 flex-col border-r border-app-border bg-app-sidebar md:flex">
      <div className="flex h-16 items-center justify-between gap-2 px-4">
        <div className="flex min-w-0 items-center gap-2.5">
          <span aria-hidden="true" className="grid size-8 shrink-0 place-items-center rounded-md bg-app-primary text-black">
            <ShieldCheck className="size-[17px]" strokeWidth={2.2} />
          </span>
          <div className="min-w-0">
            <p className="truncate font-semibold tracking-tight">Account Hub</p>
            <p className="text-[11px] text-app-subtle">账号管理</p>
          </div>
        </div>
        <Button aria-label={themeLabel} title={themeLabel} size="sm" variant="ghost" className="size-9 px-0" onClick={toggleTheme}>
          {themeIcons[mode]}</Button>
      </div>
      <nav aria-label="主导航" className="flex-1 space-y-0.5 px-2">
        {nav.map((item) => <NavLink key={item.to} to={item.to} className={({ isActive }) => cn("flex h-9 items-center gap-2.5 rounded-md px-2.5 text-sm text-app-subtle transition-colors hover:bg-app-hover hover:text-app-foreground", isActive && "bg-app-muted text-app-foreground")}>
          <span aria-hidden="true" className="grid size-5 place-items-center">
            {item.icon}</span>
          {item.label}</NavLink>)}</nav>
      <div className="border-t border-app-border p-3">
        <div className="flex items-center justify-between rounded-md px-1 py-1">
          <div>
            <p className="text-sm font-medium">
              {session?.username}</p>
            <p className="text-[11px] text-app-subtle">管理员</p>
          </div>
          <ActionMenu label="账户操作" items={accountActions} side="top" />
        </div>
      </div>
    </aside>
    <header className="sticky top-0 z-10 flex h-12 items-center justify-between border-b border-app-border bg-app-sidebar/95 px-4 backdrop-blur md:hidden">
      <Button size="sm" onClick={() => setMenuOpen(true)}>
        <Menu aria-hidden="true" className="size-3.5" />菜单</Button>
      <span className="font-semibold">Account Hub</span>
      <div className="flex items-center gap-1">
        <Button aria-label={themeLabel} title={themeLabel} size="sm" variant="ghost" className="size-9 px-0" onClick={toggleTheme}>
          {themeIcons[mode]}</Button>
        <ActionMenu label="账户操作" items={accountActions} />
      </div>
    </header>
    <Dialog open={menuOpen} onOpenChange={setMenuOpen} title="导航">
      <nav aria-label="移动端主导航" className="grid gap-2">
        {nav.map((item) => <NavLink key={item.to} to={item.to} onClick={() => setMenuOpen(false)} className={({ isActive }) => cn("flex h-9 items-center gap-2.5 rounded-md px-2.5 text-sm text-app-subtle hover:bg-app-hover hover:text-app-foreground", isActive && "bg-app-muted text-app-foreground")}>
          <span aria-hidden="true" className="grid size-5 place-items-center">
            {item.icon}</span>
          {item.label}</NavLink>)}</nav>
    </Dialog>
    <Dialog open={passwordOpen} onOpenChange={setPasswordOpen} title="修改密码" description="修改后其他登录会话将失效">
      <PasswordForm onSaved={() => setPasswordOpen(false)} />
    </Dialog>
    <ConfirmDialog open={logoutConfirmOpen} onOpenChange={setLogoutConfirmOpen} title="退出登录" description="确定要退出当前账号吗？" confirmLabel="确认退出" danger onConfirm={() => void onLogout()} />
    <main id="main-content" className="min-h-screen px-4 py-5 md:ml-56 md:px-6 lg:px-8">
      <div className="mx-auto max-w-[1440px]">
        <Outlet />
      </div>
    </main>
  </div>;
}
