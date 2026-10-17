import { useRef, useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import ArrowRight from "lucide-react/dist/esm/icons/arrow-right.mjs";
import Eye from "lucide-react/dist/esm/icons/eye.mjs";
import EyeOff from "lucide-react/dist/esm/icons/eye-off.mjs";
import KeyRound from "lucide-react/dist/esm/icons/key-round.mjs";
import Layers from "lucide-react/dist/esm/icons/layers.mjs";
import LoaderCircle from "lucide-react/dist/esm/icons/loader-circle.mjs";
import LockKeyhole from "lucide-react/dist/esm/icons/lock-keyhole.mjs";
import Moon from "lucide-react/dist/esm/icons/moon.mjs";
import RefreshCw from "lucide-react/dist/esm/icons/refresh-cw.mjs";
import ShieldCheck from "lucide-react/dist/esm/icons/shield-check.mjs";
import Sun from "lucide-react/dist/esm/icons/sun.mjs";
import UserRound from "lucide-react/dist/esm/icons/user-round.mjs";
import { Button, Card, Field, Input, useToast } from "../../components/ui";
import { errorMessage } from "../../lib/api";
import { useAuth } from "../../app/auth-context";
import { useTheme } from "../../app/theme-provider";

const loginInputClassName = "!h-10 !rounded-lg !pl-10 !text-sm transition-[border-color,box-shadow] focus:!border-app-primary focus:!ring-[3px] focus:!ring-app-primary/10 focus-visible:!outline-none";
const accountConnections = [
  { title: "GPT 账号", caption: "ACCESS TOKENS", icon: KeyRound, position: "left-0 top-[92px]", tone: "bg-app-info/10 text-app-info" },
  { title: "即梦账号", caption: "CREATIVE SPACE", icon: Layers, position: "right-0 top-3", tone: "bg-app-primary/10 text-app-primary" },
  { title: "自动刷新", caption: "ALWAYS IN SYNC", icon: RefreshCw, position: "bottom-6 right-1.5", tone: "bg-app-primary/10 text-app-primary" },
];

export function LoginPage() {
  const { session, login } = useAuth();
  const { mode, setMode } = useTheme();
  const { notify } = useToast();
  const navigate = useNavigate();
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [loading, setLoading] = useState(false);
  if (session)
    return <Navigate to="/overview" replace />;
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (loading) return;
    if (!username.trim()) {
      notify("请输入用户名", undefined, "warning");
      usernameRef.current?.focus();
      return;
    }
    if (!password) {
      notify("请输入密码", undefined, "warning");
      passwordRef.current?.focus();
      return;
    }
    setLoading(true);
    try {
      await login(username, password);
      notify("登录成功");
      navigate("/overview");
    }
    catch (reason) {
      notify("登录失败", errorMessage(reason), "danger");
    }
    finally {
      setLoading(false);
    }
  };
  const themeLabel = mode === "dark" ? "切换为浅色主题" : "切换为深色主题";

  return <main className="relative isolate flex min-h-svh flex-col overflow-hidden bg-app-background p-5 text-app-foreground sm:p-6 lg:px-[5vw] lg:py-7">
    <div aria-hidden="true" className="pointer-events-none absolute inset-0 -z-10 overflow-hidden">
      <div className="absolute left-1/4 top-1/2 size-[560px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-app-primary/10 blur-[120px]" />
      <div className="absolute -right-24 -top-40 size-[500px] rounded-full bg-app-primary/5 blur-[120px]" />
      <div className="absolute inset-0 bg-[linear-gradient(var(--border)_1px,transparent_1px),linear-gradient(90deg,var(--border)_1px,transparent_1px)] bg-[size:48px_48px] opacity-25 [mask-image:radial-gradient(ellipse_at_40%_50%,black,transparent_76%)]" />
    </div>

    <header className="mx-auto flex w-full max-w-[1280px] items-center justify-between gap-4">
      <div className="flex items-center gap-2.5 text-[17px] font-semibold tracking-[-0.6px] sm:gap-3 sm:text-[19px]">
        <span aria-hidden="true" className="grid size-9 place-items-center rounded-[11px] border border-app-primary/20 bg-app-primary/10 text-app-primary">
          <ShieldCheck className="size-[22px]" strokeWidth={1.8} />
        </span>
        <span>Account<span className="ml-[3px] text-app-primary">Hub</span></span>
      </div>
      <div className="flex items-center gap-3">
        <span className="hidden font-mono text-[10px] tracking-[1.7px] text-app-subtle lg:inline">YOUR ACCOUNT WORKSPACE</span>
        <Button type="button" variant="ghost" className="!size-9 !rounded-lg !border-app-border bg-app-surface/80 !p-0 text-app-subtle hover:!bg-app-primary/10 hover:text-app-primary focus-visible:ring-2 focus-visible:ring-app-ring" aria-label={themeLabel} title={themeLabel} onClick={() => setMode(mode === "dark" ? "light" : "dark")}>
          {mode === "dark" ? <Sun aria-hidden="true" className="size-[17px]" /> : <Moon aria-hidden="true" className="size-[17px]" />}
        </Button>
      </div>
    </header>

    <div className="mx-auto grid w-full max-w-[410px] flex-1 grid-cols-1 items-center gap-[26px] py-9 sm:gap-[30px] sm:py-10 lg:max-w-[1100px] lg:grid-cols-[minmax(0,1fr)_380px] lg:gap-12 lg:py-[50px] xl:grid-cols-[minmax(0,1fr)_400px] xl:gap-24">
      <section className="min-w-0 text-center lg:text-left" aria-labelledby="login-headline">
        <p className="mb-[18px] flex items-center justify-center gap-2.5 font-mono text-[8px] font-medium tracking-[1.3px] text-app-primary sm:text-[9px] lg:mb-[22px] lg:justify-start lg:text-[10px] lg:tracking-[1.7px]">
          <span aria-hidden="true" className="h-px w-[22px] bg-current" />ALL YOUR ACCOUNTS. IN SYNC.
        </p>
        <h1 id="login-headline" className="text-[34px] font-semibold leading-[1.25] tracking-[-1px] sm:text-[39px] lg:text-[clamp(42px,4.6vw,62px)] lg:tracking-[-2.2px]">
          连接账号，<br className="sm:hidden lg:block" /><span className="text-app-primary">掌控全局。</span>
        </h1>
        <p className="mt-4 hidden text-[12px] leading-[1.9] text-app-subtle sm:block lg:mt-[22px] lg:text-[14px]">Token、账号与自动化任务，<br className="hidden lg:block" />在一个工作台里有序运转。</p>

        <div className="relative mt-6 hidden h-[270px] w-full max-w-[480px] lg:block" aria-hidden="true">
          <span className="absolute left-1/2 top-[49%] size-64 -translate-x-1/2 -translate-y-1/2 rounded-full border border-dashed border-app-primary/15">
            <span className="absolute inset-0 animate-spin rounded-full [animation-duration:34s] motion-reduce:animate-none">
              <span className="absolute -top-[3px] left-1/2 size-1.5 -translate-x-1/2 rounded-full bg-app-primary shadow-[0_0_12px_var(--primary)]" />
            </span>
          </span>
          <span className="absolute left-1/2 top-[49%] size-[184px] -translate-x-1/2 -translate-y-1/2 rounded-full border border-app-primary/25">
            <span className="absolute inset-0 animate-spin rounded-full [animation-direction:reverse] [animation-duration:24s] motion-reduce:animate-none">
              <span className="absolute -top-[3px] left-1/2 size-1.5 -translate-x-1/2 rounded-full bg-app-primary shadow-[0_0_12px_var(--primary)]" />
            </span>
          </span>
          <span className="absolute left-1/2 top-[49%] size-28 -translate-x-1/2 -translate-y-1/2 rounded-full border border-app-primary/15 bg-app-primary/5" />
          <span className="absolute left-1/2 top-[49%] h-px w-[135px] origin-left rotate-180 bg-linear-to-r from-app-primary/40 to-app-primary/10" />
          <span className="absolute left-1/2 top-[49%] h-px w-[150px] origin-left -rotate-[39deg] bg-linear-to-r from-app-primary/40 to-app-primary/10" />
          <span className="absolute left-1/2 top-[49%] h-px w-40 origin-left rotate-[33deg] bg-linear-to-r from-app-primary/40 to-app-primary/10" />
          <div className="absolute left-1/2 top-[49%] grid h-[78px] w-[72px] -translate-x-1/2 -translate-y-1/2 content-center justify-items-center gap-1 rounded-[20px] border border-app-primary/40 bg-app-surface text-app-primary shadow-2xl shadow-app-primary/15 ring-8 ring-app-primary/5">
            <ShieldCheck className="size-[29px]" strokeWidth={1.5} /><span className="font-mono text-[9px] font-semibold tracking-[2px]">HUB</span>
          </div>
          {accountConnections.map(({ title, caption, icon: Icon, position, tone }) => <div key={title} className={`absolute flex items-center gap-2 rounded-xl border border-app-border bg-app-surface/95 p-2.5 shadow-lg shadow-black/5 xl:gap-[11px] xl:px-[15px] xl:py-3 ${position}`}>
            <span className={`grid size-[30px] place-items-center rounded-lg xl:size-[34px] ${tone}`}><Icon className="size-[17px]" strokeWidth={1.7} /></span>
            <span><strong className="block text-[12px] font-medium leading-normal">{title}</strong><small className="mt-[3px] block font-mono text-[7px] tracking-[0.8px] text-app-subtle xl:text-[8px] xl:tracking-[1.1px]">{caption}</small></span>
          </div>)}
          <span className="absolute bottom-0 left-0 font-mono text-[8px] tracking-[1.4px] text-app-subtle">ONE HUB. EVERYTHING CONNECTED.</span>
        </div>
      </section>

      <Card className="relative w-full !rounded-2xl !bg-app-surface/80 p-7 text-left shadow-2xl shadow-black/5 backdrop-blur-xl sm:p-9 dark:shadow-black/40">
        <span aria-hidden="true" className="absolute -top-px inset-x-8 h-px bg-linear-to-r from-transparent via-app-primary/40 to-transparent" />
        <div>
          <span className="font-mono text-[9px] font-medium tracking-[2px] text-app-primary">WELCOME BACK</span>
          <h2 className="mt-3 text-[26px] font-semibold leading-[1.4] tracking-[-0.9px] sm:text-[28px]">登录工作台</h2>
          <p className="mt-[9px] text-[13px] leading-[1.7] text-app-subtle">欢迎回来，继续你的管理工作。</p>
        </div>
        <form className="mt-[26px] grid gap-5 sm:mt-7" aria-label="管理员登录" aria-busy={loading} noValidate onSubmit={submit}>
          <Field label="用户名">
            <div className="relative min-w-0">
              <UserRound className="pointer-events-none absolute left-3.5 top-1/2 z-10 size-4 -translate-y-1/2 text-app-subtle" aria-hidden="true" />
              <Input ref={usernameRef} className={`${loginInputClassName} !pr-3.5`} name="username" required autoComplete="username" autoCapitalize="none" spellCheck={false} placeholder="输入用户名" disabled={loading} value={username} onChange={(event) => setUsername(event.target.value)} />
            </div>
          </Field>
          <Field label="密码">
            <div className="relative min-w-0">
              <LockKeyhole className="pointer-events-none absolute left-3.5 top-1/2 z-10 size-4 -translate-y-1/2 text-app-subtle" aria-hidden="true" />
              <Input ref={passwordRef} className={`${loginInputClassName} !pr-11`} aria-label="密码" name="password" required type={showPassword ? "text" : "password"} autoComplete="current-password" placeholder="输入登录密码" disabled={loading} value={password} onChange={(event) => setPassword(event.target.value)} />
              <Button type="button" variant="ghost" className="absolute right-1 top-1/2 !size-8 -translate-y-1/2 !rounded-md border-0 !p-0 text-app-subtle hover:text-app-primary focus-visible:ring-2 focus-visible:ring-app-ring" aria-label={showPassword ? "隐藏密码" : "显示密码"} aria-pressed={showPassword} disabled={loading} onClick={() => setShowPassword((visible) => !visible)}>
                {showPassword ? <EyeOff aria-hidden="true" className="size-4" /> : <Eye aria-hidden="true" className="size-4" />}
              </Button>
            </div>
          </Field>
          <Button type="submit" variant="primary" className="group relative mt-1 !h-10 w-full !rounded-lg !text-sm font-semibold focus-visible:ring-2 focus-visible:ring-app-ring focus-visible:ring-offset-2 focus-visible:ring-offset-app-surface" disabled={loading}>
            {loading ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : null}
            <span>{loading ? "正在登录…" : "进入工作台"}</span>
            {!loading ? <ArrowRight className="absolute right-[18px] size-[18px] transition-transform group-hover:translate-x-[3px] motion-reduce:transition-none" aria-hidden="true" /> : null}
          </Button>
        </form>
        <p className="mt-[22px] flex items-center justify-center gap-[7px] border-t border-app-border pt-[18px] text-[11px] text-app-subtle"><ShieldCheck aria-hidden="true" className="size-[13px]" strokeWidth={1.7} />仅限管理员访问</p>
      </Card>
    </div>

    <footer className="mx-auto flex w-full max-w-[1280px] items-center justify-center gap-4 text-[10px] text-app-subtle sm:justify-between sm:text-[11px]">
      <span>让账号管理，更简单。</span>
      <span className="hidden items-center gap-3 font-mono text-[8px] tracking-[1.3px] sm:inline-flex">ACCOUNT HUB<span className="h-[9px] w-px bg-app-border" aria-hidden="true" />ADMIN WORKSPACE</span>
    </footer>
  </main>;
}
