import { useEffect, useState } from "react";
import { JSON_HEADERS, REAUTH, failure, send } from "../admin";

interface Field {
  value: string;
  source: "environment" | "saved" | "unset";
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
      const v: View = await res.json();
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
    const v: View = await res.json();
    setView(v);
    setForm(formOf(v));
    setNotice(v.live ? `Saved. The login page now offers “Continue with ${v.display_name.value}”.` : "Saved. Single sign-on is off.");
  }

  return (
    <section className="page">
      <h1>Sign-in</h1>
      <p>
        Let people sign in with KyIdentity or another OpenID Connect provider. Only KyIdentity's <code>kycalendar.admin</code>{" "}
        role makes an administrator; everyone from another provider is an everyday user. Switching provider or issuer disables
        the previous provider's accounts.
      </p>
      {!view.sso_enabled && <p role="status">KY_SSO_ENABLED=false: single sign-on stays off whatever you save here.</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <label htmlFor="signin-provider">Provider</label>
        <select id="signin-provider" value={form.provider} onChange={set("provider")} disabled={env(view.provider)}>
          <option value="none">None (passwords only)</option>
          <option value="kyidentity">KyIdentity</option>
          <option value="oidc">Another OpenID Connect provider</option>
        </select>
        {env(view.provider) && <EnvNote name="the KY_KYSIGNON_ variables" />}
        {form.provider !== "none" && (
          <>
            <label htmlFor="signin-name">Button label</label>
            <input id="signin-name" value={form.display_name} onChange={set("display_name")} maxLength={255} required={form.provider === "oidc"} />
            <label htmlFor="signin-issuer">Issuer URL</label>
            <input
              id="signin-issuer"
              type="url"
              value={form.issuer}
              onChange={set("issuer")}
              disabled={env(view.issuer)}
              placeholder="https://id.example.com"
              required
            />
            {env(view.issuer) && <EnvNote name="KY_KYSIGNON_ISSUER" />}
            <label htmlFor="signin-client">Client ID</label>
            <input id="signin-client" value={form.client_id} onChange={set("client_id")} disabled={env(view.client_id)} required />
            {env(view.client_id) && <EnvNote name="KY_KYSIGNON_CLIENT_ID" />}
            <label htmlFor="signin-secret">Client secret</label>
            <input
              id="signin-secret"
              type="password"
              autoComplete="off"
              value={form.client_secret}
              onChange={set("client_secret")}
              disabled={secretLocked}
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
            <button type="button" onClick={() => void test()}>
              Test
            </button>
          </>
        )}
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        <button type="submit">Save</button>
      </form>
    </section>
  );
}
