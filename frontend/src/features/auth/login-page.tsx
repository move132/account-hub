import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import ShieldCheck from "lucide-react/dist/esm/icons/shield-check.mjs";
import { Button, Card, Field, Input, useToast } from "../../components/ui";
import { errorMessage } from "../../lib/api";
import { useAuth } from "../../app/auth-context";
export function LoginPage() {
  const { session, login } = useAuth();
  const { notify } = useToast();
  const navigate = useNavigate();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  if (session)
    return <Navigate to="/tokens" replace />;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setLoading(true);
    try {
      await login(username, password);
      notify("登录成功");
      navigate("/tokens");
    }
    catch (reason) {
      notify("登录失败", errorMessage(reason), "danger");
    }
    finally {
      setLoading(false);
    }
  };
  return <main className="grid min-h-screen place-items-center bg-app-background px-4">
    <Card className="w-full max-w-[400px] p-6">
      <div className="mb-5">
        <span aria-hidden="true" className="mb-4 grid size-9 place-items-center rounded-lg bg-app-primary text-black">
          <ShieldCheck className="size-[18px] text-white" strokeWidth={2.2} />
        </span>
        <h1 className="text-balance text-2xl font-semibold tracking-tight">登录 Account Hub</h1>
        <p className="mt-1.5 text-sm text-app-subtle">管理 Token、账号与自动刷新任务</p>
      </div>
      <form className="grid gap-3" onSubmit={submit}>
        <Field label="用户名">
          <Input name="username" autoComplete="username" spellCheck={false} value={username} onChange={(event) => setUsername(event.target.value)} />
        </Field>
        <Field label="密码">
          <Input name="password" type="password" autoComplete="off" value={password} onChange={(event) => setPassword(event.target.value)} />
        </Field>
        <Button type="submit" variant="primary" size="md" disabled={loading}>
          {loading ? "登录中…" : "登录"}</Button>
      </form>
    </Card>
  </main>;
}
