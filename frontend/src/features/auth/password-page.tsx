import { useState, type FormEvent } from "react";
import { Button, Card, CardHeader, Field, Input, PageHeader, useToast } from "../../components/ui";
import { api, errorMessage, jsonBody } from "../../lib/api";
export function PasswordForm({ onSaved }: {
  onSaved?: () => void;
}) {
  const { notify } = useToast();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [saving, setSaving] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      await api("/api/v1/auth/password", {
        method: "PATCH",
        ...jsonBody({
          current_password: currentPassword,
          new_password: newPassword,
          confirm_password: confirmPassword,
        }),
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      notify("密码修改成功");
      onSaved?.();
    }
    catch (error) {
      notify("密码修改失败", errorMessage(error), "danger");
    }
    finally {
      setSaving(false);
    }
  };
  return <form className="grid gap-3" onSubmit={submit}>
    <Field label="当前密码">
      <Input name="current-password" autoComplete="off" type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
    </Field>
    <Field label="新密码" hint="至少 12 个字符">
      <Input name="new-password" autoComplete="off" type="password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} />
    </Field>
    <Field label="确认新密码">
      <Input name="confirm-password" autoComplete="off" type="password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} />
    </Field>
    <div className="flex justify-end gap-2">
      <Button type="submit" variant="primary" disabled={saving}>
        {saving ? "保存中..." : "保存密码"}</Button>
    </div>
  </form>;
}
export function PasswordPage() {
  return <>
    <PageHeader title="修改密码" description="管理员密码只以 Argon2id 哈希保存" />
    <Card className="max-w-xl">
      <CardHeader title="登录密码" description="修改后其他登录会话将失效" />
      <div className="p-4">
        <PasswordForm />
      </div>
    </Card>
  </>;
}
