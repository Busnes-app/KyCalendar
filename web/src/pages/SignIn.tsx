import { useEffect, useState } from "react";
import { JSON_HEADERS, REAUTH, failure, send } from "../admin";

interface Field {
  value: string;
  source: "environment" | "saved" | "unset" | "installer";
}

interface View {
  provider: Field;
  display_name: Field;
  issuer: Field;
  client_id: Field;
  client_secret: { set: boolean; source: string };
  callback_url: string;
  sso_enabled: boolean;
  live: boolean;
  managed: boolean;
}

function isField(value: unknown): value is Field {
  return typeof value === "object" && value !== null && "value" in value && typeof value.value === "string" &&
    "source" in value && ["environment", "saved", "unset", "installer"].some((source) => source === value.source);
}

function isView(value: unknown): value is View {
  if (typeof value !== "object" || value === null) return false;
  return "provider" in value && isField(value.provider) && "display_name" in value && isField(value.display_name) &&
    "issuer" in value && isField(value.issuer) && "client_id" in value && isField(value.client_id) &&
    "client_secret" in value && typeof value.client_secret === "object" && value.client_secret !== null &&
    "set" in value.client_secret && typeof value.client_secret.set === "boolean" &&
    "source" in value.client_secret && typeof value.client_secret.source === "string" &&
    "callback_url" in value && typeof value.callback_url === "string" &&
    "sso_enabled" in value && typeof value.sso_enabled === "boolean" && "live" in value && typeof value.live === "boolean" &&
    "managed" in value && typeof value.managed === "boolean";
}

async function readView(res: Response): Promise<View | null> {
  try { const value: unknown = await res.json(); return isView(value) ? value : null; } catch { return null; }
}

interface Form {
  provider: string;
  display_name: string;
  issuer: string;
  client_id: string;
  client_secret: string;
}

function formOf(v: View): Form {
  return { provider: v.provider.value, display_name: v.display_name.value, issuer: v.issuer.value, client_id: v.client_id.value, client_secret: "" };
}

function EnvNote({ name }: { name: string }) {
  return <small>Set by the environment ({name}); change it there.</small>;
}

export default function SignIn() {
  const [view, setView] = useState<View | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      const res = await send("/api/admin/signin");
      if (!res?.ok) {
        setError("Could not load the sign-in settings. Reload the page to try again.");
        return;
      }
      const v = await readView(res);
      if (!v) { setError("Invalid sign-in settings response. Reload the page to try again."); return; }
      setView(v);
      setForm(formOf(v));
    })();
  }, []);

  if (!view || !form) {
    return (
      <section className="page">
        <h1>Sign-in</h1>
        {error && <p role="alert">{error}</p>}
      </section>
    );
  }

  const env = (f: Field) => f.source === "environment";
  const secretLocked = view.client_secret.source === "environment";
  // The server keeps a stored secret only for the same registration; issuers compare exactly.
  const keepsSecret =
    view.client_secret.set &&
    form.provider === view.provider.value &&
    form.issuer.trim() === view.issuer.value &&
    form.client_id === view.client_id.value;
  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setError(null);
    } catch {
      setError("Could not copy; select the address instead.");
    }
  }

  async function test() {
    if (view?.managed) return;
    setError(null);
    setNotice(null);
    const res = await send("/api/admin/signin/test", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "The test failed");
      return;
    }
    setNotice("The provider answered correctly. Save to use it.");
  }

  async function save(confirmed = 0) {
    if (view?.managed) return;
    setError(null);
    setNotice(null);
    const res = await send("/api/admin/signin", { method: "PUT", headers: JSON_HEADERS, body: JSON.stringify({ ...form, confirm_disable: confirmed }) });
    if (!res?.ok) {
      const f = await failure(res);
      if (f.code === "confirm_provider_change" && f.count !== undefined) {
        const accounts = f.count === 1 ? "1 account" : `${f.count} accounts`;
        if (window.confirm(`Switching provider or issuer disables ${accounts} from the previous provider. They keep their calendars but cannot sign in again. Continue?`)) {
          await save(f.count);
        }
        return;
      }
      setError(f.code === "reauth_required" ? REAUTH : (f.error ?? "Could not save the sign-in settings"));
      return;
    }
    const v = await readView(res);
    if (!v) { setError("Invalid sign-in settings response. Reload the page to try again."); return; }
    setView(v);
    setForm(formOf(v));
    setNotice(v.live ? `Saved. The login page now offers “Continue with ${v.display_name.value}”.` : "Saved. Single sign-on is off.");
  }

  return (
    <section className="page">
      <h1>Sign-in</h1>
      <p>
        Let people sign in with Single Sign On or another OpenID Connect provider. Only Single Sign On's <code>kycalendar.admin</code>{" "}
        role makes an administrator; everyone from another provider is an everyday user. Switching provider or issuer disables
        the previous provider's accounts.
      </p>
      {!view.sso_enabled && <p role="status">KY_SSO_ENABLED=false: single sign-on stays off whatever you save here.</p>}
      {view.managed && <p role="status">Sign-in is managed by KyQuickStart. Change it through the installer.</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <label htmlFor="signin-provider">Provider</label>
        <select id="signin-provider" value={form.provider} onChange={set("provider")} disabled={view.managed || env(view.provider)}>
          <option value="none">None (passwords only)</option>
          <option value="kyidentity">Single Sign On</option>
          <option value="oidc">Another OpenID Connect provider</option>
        </select>
        {env(view.provider) && <EnvNote name="the KY_KYSIGNON_ variables" />}
        {form.provider !== "none" && (
          <>
            <label htmlFor="signin-name">Button label</label>
            <input id="signin-name" disabled={view.managed} value={form.display_name} onChange={set("display_name")} maxLength={255} required={form.provider === "oidc"} />
            <label htmlFor="signin-issuer">Issuer URL</label>
            <input
              id="signin-issuer"
              type="url"
              value={form.issuer}
              onChange={set("issuer")}
              disabled={view.managed || env(view.issuer)}
              placeholder="https://id.example.com"
              required
            />
            {env(view.issuer) && <EnvNote name="KY_KYSIGNON_ISSUER" />}
            <label htmlFor="signin-client">Client ID</label>
            <input id="signin-client" value={form.client_id} onChange={set("client_id")} disabled={view.managed || env(view.client_id)} required />
            {env(view.client_id) && <EnvNote name="KY_KYSIGNON_CLIENT_ID" />}
            <label htmlFor="signin-secret">Client secret</label>
            <input
              id="signin-secret"
              type="password"
              autoComplete="off"
              value={form.client_secret}
              onChange={set("client_secret")}
              disabled={view.managed || secretLocked}
              required={!secretLocked && !keepsSecret}
              placeholder={keepsSecret ? "Stored; leave blank to keep it" : "Required"}
            />
            {secretLocked && <EnvNote name="KY_KYSIGNON_SECRET" />}
            <p>
              Redirect URI to register with the provider: <code>{view.callback_url}</code>{" "}
              <button type="button" onClick={() => void copy(view.callback_url)}>
                Copy
              </button>
            </p>
            <button type="button" disabled={view.managed} onClick={() => void test()}>
              Test
            </button>
          </>
        )}
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        <button type="submit" disabled={view.managed}>Save</button>
      </form>
    </section>
  );
}
