import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";
import { REAUTH } from "../admin";
import { CodeBlock, CopyButton, EmptyState, PageHeader } from "../components/ui";

type AgentScope = "mcp:read" | "mcp:write-own" | "mcp:write-any";
type Scope = "dav" | AgentScope;

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

// The three agent access levels, narrowest first; the create form and each agent's row use these names.
const ACCESS: { scope: AgentScope; name: string; hint: string }[] = [
  { scope: "mcp:read", name: "Read only", hint: "Sees your calendars and events. Changes nothing." },
  { scope: "mcp:write-own", name: "Edit its own events", hint: "Adds events, and changes only the ones it added." },
  { scope: "mcp:write-any", name: "Edit any event", hint: "Adds events, and changes any of your events." },
];

const isAgent = (s: Scope) => s.startsWith("mcp:");

const WRITE_WARNING =
  "The agent can add and change events. Anyone who can message the agent can try to make it do so. It can never delete events.";
const ANY_WARNING = "The agent can then move or rewrite any of your appointments.";

// widening is the warning for moving an agent from one access to more, or null when it narrows.
function widening(from: Scope, to: Scope): string | null {
  const rank: Scope[] = ACCESS.map((a) => a.scope);
  if (rank.indexOf(to) <= rank.indexOf(from)) return null;
  return [from === "mcp:read" && WRITE_WARNING, to === "mcp:write-any" && ANY_WARNING].filter(Boolean).join(" ");
}

export default function AppPasswords({ username }: { username: string }) {
  const [list, setList] = useState<AppPassword[]>([]);
  const [label, setLabel] = useState("");
  const [agent, setAgent] = useState(false);
  const [access, setAccess] = useState<AgentScope>("mcp:read");
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

  const scope: Scope = agent ? access : "dav";

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

  const host = window.location.host;

  return (
    <section>
      <PageHeader title="Phones and apps" lede="Each device or AI agent gets its own app password. Revoke one and only that one stops working." />

      <div className="card">
        <h2 className="section-title">Add a device or agent</h2>
        <form onSubmit={create} className="form form-wide">
          <fieldset>
            <legend>Kind</legend>
            <label className="choice">
              <input type="radio" name="kind" checked={!agent} onChange={() => setAgent(false)} />
              <span>
                Calendar app (CalDAV)
                <span className="choice-hint">iPhone, iPad, Android with DAVx5, Thunderbird</span>
              </span>
            </label>
            <label className="choice">
              <input type="radio" name="kind" checked={agent} onChange={() => setAgent(true)} />
              <span>
                AI agent
                <span className="choice-hint">OpenClaw, Hermes Agent</span>
              </span>
            </label>
          </fieldset>
          {agent && (
            <fieldset>
              <legend>Agent access</legend>
              {ACCESS.map((a) => (
                <label className="choice" key={a.scope}>
                  <input type="radio" name="access" value={a.scope} checked={access === a.scope} onChange={() => setAccess(a.scope)} />
                  <span>
                    {a.name}
                    <span className="choice-hint">{a.hint}</span>
                  </span>
                </label>
              ))}
              {access !== "mcp:read" && <p role="note">{widening("mcp:read", access)}</p>}
            </fieldset>
          )}
          <div className="field">
            <label htmlFor="app-label">{agent ? "Agent name" : "Device name"}</label>
            <input id="app-label" value={label} onChange={(e) => setLabel(e.target.value)} maxLength={64} placeholder={agent ? "For example, OpenClaw on laptop" : "For example, iPhone"} required />
          </div>
          <div className="form-actions">
            <button type="submit">Create</button>
          </div>
        </form>
      </div>
      {error && <p role="alert" className="section">{error}</p>}

      {created &&
        (isAgent(created.scope) ? (
          <AgentSetup token={created.password} onDone={() => setCreated(null)} />
        ) : (
          <div role="status" className="card card-created">
            <h2 className="section-title">Enter these on the device now</h2>
            <p className="section-lede">The password is not shown again.</p>
            <dl className="facts">
              <dt>Server</dt>
              <dd>{host}</dd>
              <dt>Username</dt>
              <dd>{username}</dd>
              <dt>Password</dt>
              <dd>
                <div className="secret">
                  <code>{created.password}</code>
                  <CopyButton text={created.password} label="Copy the app password" />
                </div>
              </dd>
            </dl>
            <div className="form-actions card-footer">
              <button type="button" onClick={() => setCreated(null)}>
                Done
              </button>
            </div>
          </div>
        ))}

      <section className="section" aria-labelledby="devices-title">
        <h2 className="section-title" id="devices-title">Devices and agents</h2>
        {list.length === 0 ? (
          <EmptyState title="No app passwords yet.">Add your phone, a desktop calendar or an AI agent above.</EmptyState>
        ) : (
          <div className="table-card">
            <table className="data-table stacked">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Access</th>
                  <th>Last used</th>
                  <th className="col-actions">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <span className="cell-strong">{p.label}</span>
                      <span className="cell-sub">{isAgent(p.scope) ? "AI agent" : "Calendar app"}</span>
                    </td>
                    <td data-label="Access">
                      {isAgent(p.scope) ? (
                        <>
                          <select
                            className="select-compact"
                            aria-label={`Access for ${p.label}`}
                            value={p.scope}
                            onChange={(e) => void changeAccess(p, e.target.value as Scope)}
                          >
                            {ACCESS.map((a) => (
                              <option key={a.scope} value={a.scope}>
                                {a.name}
                              </option>
                            ))}
                          </select>
                          {p.revoke_after && <span className="cell-sub">Revoked if unused by {new Date(p.revoke_after).toLocaleDateString()}</span>}
                        </>
                      ) : (
                        "Calendars and events (CalDAV)"
                      )}
                    </td>
                    <td data-label="Last used" className="cell-nowrap">
                      {p.last_used_at ? new Date(p.last_used_at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }) : <span className="muted">Never</span>}
                    </td>
                    <td className="col-actions">
                      <button type="button" className="btn-danger btn-sm" aria-label={`Revoke ${p.label}`} onClick={() => revoke(p.id)}>
                        Revoke
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="section" aria-labelledby="setup-title">
        <h2 className="section-title" id="setup-title">Setup</h2>
        <div className="card">
          <dl className="facts facts-setup">
            <dt>iPhone and iPad</dt>
            <dd>Settings → Calendar → Accounts → Add Account → Other → Add CalDAV Account.</dd>
            <dt>Android</dt>
            <dd>
              DAVx5 → Add account → Login with URL and user name, URL <code>https://{host}/</code>.
            </dd>
            <dt>Thunderbird</dt>
            <dd>
              New Calendar → On the Network, location <code>https://{host}/</code>.
            </dd>
            <dt>AI agents</dt>
            <dd>Create an AI agent password above; the setup for OpenClaw and Hermes Agent is shown with it.</dd>
          </dl>
        </div>
      </section>
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
    <div role="status" className="card card-created">
      <h2 className="section-title">Set up the agent now</h2>
      <p className="section-lede">Set this as KYCAL_TOKEN in the agent's environment now. The token is not shown again.</p>
      <div className="secret">
        <code>{token}</code>
        <CopyButton text={token} label="Copy the agent token" />
      </div>
      <p className="field-hint">The agent loses access if it does not use KyCalendar for 90 days; create a new token then.</p>
      <div className="setup-grid">
        <section aria-labelledby="setup-openclaw">
          <h3 className="setup-title" id="setup-openclaw">OpenClaw</h3>
          <p className="field-hint">
            Add to <code>openclaw.json</code>:
          </p>
          <CodeBlock code={openclaw} label="OpenClaw configuration" />
          <p className="field-hint">Install the skill:</p>
          <CodeBlock code={`mkdir -p ~/.agents/skills/kycalendar\ncurl -fsSo ~/.agents/skills/kycalendar/SKILL.md ${skill}`} label="OpenClaw skill install commands" />
          <p className="field-hint">
            With a custom <code>OPENCLAW_STATE_DIR</code>, use <code>&lt;workspace&gt;/skills</code> instead.
          </p>
        </section>
        <section aria-labelledby="setup-hermes">
          <h3 className="setup-title" id="setup-hermes">Hermes Agent</h3>
          <p className="field-hint">
            Add to <code>~/.hermes/config.yaml</code>:
          </p>
          <CodeBlock code={hermes} label="Hermes Agent configuration" />
          <p className="field-hint">Install the skill:</p>
          <CodeBlock code={`hermes skills install ${skill} --category productivity`} label="Hermes Agent skill install command" />
        </section>
      </div>
      <div className="form-actions card-footer">
        <button type="button" onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  );
}
