import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";
import { REAUTH } from "../admin";

type Scope = "dav" | "mcp:read" | "mcp:write-own" | "mcp:write-any";

interface AppPassword {
  id: string;
  label: string;
  scope: Scope;
  created_at: string;
  last_used_at: string | null;
  revoke_after?: string;
}

interface Created {
  password: string;
  scope: Scope;
}

const ACCESS: Record<Exclude<Scope, "dav">, string> = {
  "mcp:read": "Read only",
  "mcp:write-own": "Read and write: only events it created",
  "mcp:write-any": "Read and write: all events",
};

const isAgent = (s: Scope) => s.startsWith("mcp:");

const WRITE_WARNING =
  "The agent can add and change events. Anyone who can message the agent can try to make it do so. It can never delete events.";
const ANY_WARNING = "The agent can then move or rewrite any of your appointments.";

// widening is the warning for moving an agent from one access to more, or null when it narrows.
function widening(from: Scope, to: Scope): string | null {
  const rank = ["mcp:read", "mcp:write-own", "mcp:write-any"];
  if (rank.indexOf(to) <= rank.indexOf(from)) return null;
  return [from === "mcp:read" && WRITE_WARNING, to === "mcp:write-any" && ANY_WARNING].filter(Boolean).join(" ");
}

export default function AppPasswords({ username }: { username: string }) {
  const [list, setList] = useState<AppPassword[]>([]);
  const [label, setLabel] = useState("");
  const [agent, setAgent] = useState(false);
  const [write, setWrite] = useState(false);
  const [anyEvent, setAnyEvent] = useState(false);
  const [created, setCreated] = useState<Created | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Resolves to null on a network error so every caller handles one failure path.
  const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

  const load = useCallback(async () => {
    const res = await secureFetch("/api/app-passwords").catch(() => null);
    if (!res?.ok) {
      setError("Could not load your app passwords. Reload the page to try again.");
      return;
    }
    setList(await res.json());
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const scope: Scope = !agent ? "dav" : !write ? "mcp:read" : anyEvent ? "mcp:write-any" : "mcp:write-own";

  async function failed(res: Response | null, fallback: string) {
    const body = await res?.json().catch(() => null);
    setError(body?.code === "reauth_required" ? REAUTH : (body?.error ?? fallback));
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/app-passwords", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label, scope }),
    });
    if (!res?.ok) {
      await failed(res, "Could not create the app password");
      return;
    }
    setCreated({ password: (await res.json()).password, scope });
    setLabel("");
    await load();
  }

  async function revoke(id: string) {
    setError(null);
    const res = await send(`/api/app-passwords/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (!res?.ok) {
      setError("Could not revoke the app password. It still works; try again.");
      return;
    }
    await load();
  }

  async function changeAccess(p: AppPassword, next: Scope) {
    const warning = widening(p.scope, next);
    if (warning && !window.confirm(`${warning}\n\nGive ${p.label} this access?`)) return;
    setError(null);
    const res = await send(`/api/app-passwords/${encodeURIComponent(p.id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scope: next }),
    });
    if (!res?.ok) {
      await failed(res, "Could not change the agent's access. It keeps its old access.");
    }
    await load();
  }

  return (
    <section className="page">
      <h1>Phones and apps</h1>
      <p>Each device or AI agent gets its own app password. Revoke one and only that one stops working.</p>

      <form onSubmit={create} className="kc-form">
        <fieldset>
          <legend>Kind</legend>
          <label className="kc-check">
            <input type="radio" name="kind" checked={!agent} onChange={() => setAgent(false)} />
            Calendar app (CalDAV)
          </label>
          <label className="kc-check">
            <input type="radio" name="kind" checked={agent} onChange={() => setAgent(true)} />
            AI agent (OpenClaw, Hermes Agent)
          </label>
        </fieldset>
        {agent && (
          <fieldset>
            <legend>Agent access</legend>
            <label className="kc-check">
              <input type="radio" name="access" checked={!write} onChange={() => setWrite(false)} />
              Read only
            </label>
            <label className="kc-check">
              <input type="radio" name="access" checked={write} onChange={() => setWrite(true)} />
              Read and write
            </label>
            {write && (
              <>
                <p role="note">{WRITE_WARNING}</p>
                <label className="kc-check">
                  <input type="checkbox" checked={anyEvent} onChange={(e) => setAnyEvent(e.target.checked)} />
                  Can also change events it did not create
                </label>
                {anyEvent && <p role="note">{ANY_WARNING}</p>}
              </>
            )}
          </fieldset>
        )}
        <label>
          {agent ? "Agent name" : "Device name"}
          <input value={label} onChange={(e) => setLabel(e.target.value)} maxLength={64} required />
        </label>
        <div className="kc-actions">
          <button type="submit">Create</button>
        </div>
      </form>
      {error && <p role="alert">{error}</p>}

      {created &&
        (isAgent(created.scope) ? (
          <AgentSetup token={created.password} onDone={() => setCreated(null)} />
        ) : (
          <div role="status">
            <p>Enter these on the device now. The password is not shown again.</p>
            <dl>
              <dt>Server</dt>
              <dd>{window.location.host}</dd>
              <dt>Username</dt>
              <dd>{username}</dd>
              <dt>Password</dt>
              <dd>
                <code>{created.password}</code>
              </dd>
            </dl>
            <button type="button" onClick={() => setCreated(null)}>
              Done
            </button>
          </div>
        ))}

      <h2>Setup</h2>
      <ul>
        <li>iPhone and iPad: Settings → Calendar → Accounts → Add Account → Other → Add CalDAV Account.</li>
        <li>
          Android: DAVx5 → Add account → Login with URL and user name, URL <code>https://{window.location.host}/</code>.
        </li>
        <li>
          Thunderbird: New Calendar → On the Network, location <code>https://{window.location.host}/</code>.
        </li>
        <li>AI agents: create an AI agent password above; the setup for OpenClaw and Hermes Agent is shown with it.</li>
      </ul>

      <h2>Devices and agents</h2>
      {list.length === 0 ? (
        <p>No app passwords yet.</p>
      ) : (
        <ul>
          {list.map((p) => (
            <li key={p.id}>
              <span>{p.label}</span>{" "}
              <small>{p.last_used_at ? `last used ${new Date(p.last_used_at).toLocaleString()}` : "never used"}</small>{" "}
              {isAgent(p.scope) && (
                <>
                  <select
                    aria-label={`Access for ${p.label}`}
                    value={p.scope}
                    onChange={(e) => void changeAccess(p, e.target.value as Scope)}
                  >
                    {Object.entries(ACCESS).map(([value, name]) => (
                      <option key={value} value={value}>
                        {name}
                      </option>
                    ))}
                  </select>{" "}
                  {p.revoke_after && <small>Revoked if unused by {new Date(p.revoke_after).toLocaleDateString()}</small>}{" "}
                </>
              )}
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

// AgentSetup shows a new agent token once, with copy-ready config that reads it from KYCAL_TOKEN,
// so the token itself never lands in a config file.
function AgentSetup({ token, onDone }: { token: string; onDone: () => void }) {
  const origin = window.location.origin;
  const skill = `${origin}/agent/skills/kycalendar/SKILL.md`;
  const openclaw = `"mcp": { "servers": { "kycalendar": {
  "url": "${origin}/mcp",
  "transport": "streamable-http",
  "headers": { "Authorization": "Bearer \${KYCAL_TOKEN}" }
}}}`;
  const hermes = `mcp_servers:
  kycalendar:
    url: "${origin}/mcp"
    headers:
      Authorization: "Bearer \${KYCAL_TOKEN}"`;
  return (
    <div role="status">
      <p>Set this as KYCAL_TOKEN in the agent's environment now. The token is not shown again.</p>
      <p>
        <code>{token}</code>
      </p>
      <p>The agent loses access if it does not use KyCalendar for 90 days; create a new token then.</p>
      <h3>OpenClaw</h3>
      <p>
        Add to <code>openclaw.json</code>:
      </p>
      <pre>
        <code>{openclaw}</code>
      </pre>
      <p>Install the skill:</p>
      <pre>
        <code>{`mkdir -p ~/.agents/skills/kycalendar\ncurl -fsSo ~/.agents/skills/kycalendar/SKILL.md ${skill}`}</code>
      </pre>
      <p>
        With a custom <code>OPENCLAW_STATE_DIR</code>, use <code>&lt;workspace&gt;/skills</code> instead.
      </p>
      <h3>Hermes Agent</h3>
      <p>
        Add to <code>~/.hermes/config.yaml</code>:
      </p>
      <pre>
        <code>{hermes}</code>
      </pre>
      <p>Install the skill:</p>
      <pre>
        <code>{`hermes skills install ${skill} --category productivity`}</code>
      </pre>
      <button type="button" onClick={onDone}>
        Done
      </button>
    </div>
  );
}
