import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";

interface AppPassword {
  id: string;
  label: string;
  created_at: string;
  last_used_at: string | null;
}

export default function AppPasswords({ username }: { username: string }) {
  const [list, setList] = useState<AppPassword[]>([]);
  const [label, setLabel] = useState("");
  const [created, setCreated] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const res = await secureFetch("/api/app-passwords");
    if (res.ok) setList(await res.json());
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await secureFetch("/api/app-passwords", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label }),
    });
    if (!res.ok) {
      setError((await res.json().catch(() => null))?.error ?? "Could not create the app password");
      return;
    }
    setCreated((await res.json()).password);
    setLabel("");
    await load();
  }

  async function revoke(id: string) {
    await secureFetch(`/api/app-passwords/${encodeURIComponent(id)}`, { method: "DELETE" });
    await load();
  }

  return (
    <section className="page">
      <h1>Phones and apps</h1>
      <p>Each device gets its own app password. Revoke one and only that device stops syncing.</p>

      <form onSubmit={create}>
        <label>
          Device name
          <input value={label} onChange={(e) => setLabel(e.target.value)} maxLength={64} required />
        </label>
        <button type="submit">Create</button>
      </form>
      {error && <p role="alert">{error}</p>}

      {created && (
        <div role="status">
          <p>Enter these on the device now. The password is not shown again.</p>
          <dl>
            <dt>Server</dt>
            <dd>{window.location.host}</dd>
            <dt>Username</dt>
            <dd>{username}</dd>
            <dt>Password</dt>
            <dd><code>{created}</code></dd>
          </dl>
          <button type="button" onClick={() => setCreated(null)}>Done</button>
        </div>
      )}

      <h2>Setup</h2>
      <ul>
        <li>iPhone and iPad: Settings → Calendar → Accounts → Add Account → Other → Add CalDAV Account.</li>
        <li>Android: DAVx5 → Add account → Login with URL and user name, URL <code>https://{window.location.host}/</code>.</li>
        <li>Thunderbird: New Calendar → On the Network, location <code>https://{window.location.host}/</code>.</li>
      </ul>

      <h2>Devices</h2>
      {list.length === 0 ? (
        <p>No app passwords yet.</p>
      ) : (
        <ul>
          {list.map((p) => (
            <li key={p.id}>
              <span>{p.label}</span>{" "}
              <small>{p.last_used_at ? `last used ${new Date(p.last_used_at).toLocaleString()}` : "never used"}</small>{" "}
              <button type="button" aria-label={`Revoke ${p.label}`} onClick={() => revoke(p.id)}>
                Revoke
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
