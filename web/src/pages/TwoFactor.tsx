import { useState } from "react";
import { JSON_HEADERS, send } from "../admin";
import { CopyButton, PageHeader } from "../components/ui";

interface Pending {
  secret: string;
  otpauth_uri: string;
}

async function errorOf(res: Response | null, fallback: string): Promise<string> {
  const body = await res?.json().catch(() => null);
  return body?.error ?? fallback;
}

/**
 * Two-step verification for a local account: start with the password, add the secret to an
 * authenticator app, confirm with its first code, keep the recovery codes. Turning it off needs
 * the password and a current code.
 */
export default function TwoFactor({ enabled, onChange }: { enabled: boolean; onChange: (enabled: boolean) => void }) {
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function post(path: string, body: object, fallback: string): Promise<any | null> {
    setError(null);
    setBusy(true);
    const res = await send(path, { method: "POST", headers: JSON_HEADERS, body: JSON.stringify(body) });
    setBusy(false);
    if (!res?.ok) {
      setError(await errorOf(res, fallback));
      return null;
    }
    return res.json();
  }

  async function start(e: React.FormEvent) {
    e.preventDefault();
    const out = await post("/api/auth/mfa/enroll", { password }, "Could not start two-step verification");
    if (out) {
      setPending(out);
      setPassword("");
    }
  }

  async function confirm(e: React.FormEvent) {
    e.preventDefault();
    const out = await post("/api/auth/mfa/enroll/confirm", { code: code.trim() }, "Could not confirm the code");
    if (out) {
      setPending(null);
      setCode("");
      setRecovery(out.recovery_codes);
      onChange(true);
    }
  }

  async function disable(e: React.FormEvent) {
    e.preventDefault();
    const out = await post("/api/auth/mfa/disable", { password, code: code.trim() }, "Could not turn off two-step verification");
    if (out) {
      setPassword("");
      setCode("");
      onChange(false);
    }
  }

  const passwordField = (
    <div className="field">
      <label htmlFor="mfa-password">Password</label>
      <input id="mfa-password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
    </div>
  );
  const codeField = (label: string) => (
    <div className="field">
      <label htmlFor="mfa-setup-code">{label}</label>
      <input id="mfa-setup-code" className="code-input" type="text" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} required />
    </div>
  );

  return (
    <section className="page">
      <PageHeader title="Two-step verification" lede="Sign-in asks for a code from an authenticator app as well as your password." />
      {error && <p role="alert" className="section">{error}</p>}

      {recovery && (
        <div role="status" className="card card-created">
          <h2 className="section-title">Save your recovery codes</h2>
          <p className="section-lede">Each code signs you in once if you lose your authenticator. They are not shown again.</p>
          <div className="secret">
            <code>{recovery.join(" ")}</code>
            <CopyButton text={recovery.join("\n")} label="Copy the recovery codes" />
          </div>
          <div className="form-actions card-footer">
            <button type="button" onClick={() => setRecovery(null)}>Done</button>
          </div>
        </div>
      )}

      {enabled && !recovery && (
        <div className="card">
          <h2 className="section-title">Two-step verification is on</h2>
          <form onSubmit={disable} className="form">
            {passwordField}
            {codeField("Authenticator or recovery code")}
            <div className="form-actions">
              <button type="submit" className="btn-danger" disabled={busy}>Turn off</button>
            </div>
          </form>
        </div>
      )}

      {!enabled && !pending && (
        <div className="card">
          <h2 className="section-title">Two-step verification is off</h2>
          <form onSubmit={start} className="form">
            {passwordField}
            <div className="form-actions">
              <button type="submit" disabled={busy}>Set up</button>
            </div>
          </form>
        </div>
      )}

      {!enabled && pending && (
        <div className="card">
          <h2 className="section-title">Add this account to your authenticator app</h2>
          <p className="section-lede">
            On your phone, open the <a href={pending.otpauth_uri}>setup link</a>, or enter this key in the app by hand.
          </p>
          <div className="secret">
            <code>{pending.secret}</code>
            <CopyButton text={pending.secret} label="Copy the setup key" />
          </div>
          <form onSubmit={confirm} className="form">
            {codeField("Code from the app")}
            <div className="form-actions">
              <button type="submit" disabled={busy}>Turn on</button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
}
