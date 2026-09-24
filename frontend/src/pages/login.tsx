import { useState } from "react";
import { api } from "@/lib/api";
import { Button, Input } from "@/components/ui";

export function Login() {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <div className="grid min-h-screen place-items-center bg-sidebar px-4">
      <form
        className="w-full max-w-sm rounded-2xl border border-white/10 bg-card p-8 text-card-foreground shadow-2xl"
        onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          try {
            await api("/api/auth/login", { method: "POST", body: JSON.stringify({ password }) });
            location.href = "/";
          } catch (err) {
            setError(err instanceof Error ? err.message : "登录失败");
          } finally {
            setBusy(false);
          }
        }}
      >
        <div className="mb-6 flex items-center gap-3">
          <span className="grid h-10 w-10 place-items-center rounded-lg bg-primary font-bold text-primary-foreground">IC</span>
          <div>
            <h1 className="text-lg font-semibold">ICPC 远程集控</h1>
            <p className="text-xs text-muted-foreground">管理员登录</p>
          </div>
        </div>
        <Input type="password" autoFocus placeholder="密码" value={password} onChange={(event) => setPassword(event.target.value)} />
        {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
        <Button className="mt-4 w-full" disabled={busy || !password}>{busy ? "正在登录…" : "进入控制台"}</Button>
      </form>
    </div>
  );
}
