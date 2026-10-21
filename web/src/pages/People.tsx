import { useCallback, useEffect, useRef, useState } from "react";
import { UserPlus } from "lucide-react";
import { JSON_HEADERS, LoadMore, REAUTH, SourceBadge, failure, pageURL, send } from "../admin";
import { CopyButton, EmptyState, Modal, PageHeader } from "../components/ui";

interface Person {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: string;
  status: string;
  source: string;
  mfa: boolean;
  must_change_password: boolean;
  last_login_at: string | null;
  bound_to?: string;
  needs_reattach: boolean;
}

interface Handover {
  username: string;
  password: string;
}

type Action = "reset-password" | "role" | "disable" | "enable" | "reattach";

async function refusal(res: Response | null, fallback: string): Promise<string> {
  const f = await failure(res);
  return f.code === "reauth_required" ? REAUTH : (f.error ?? fallback);
}

export default function People({ me }: { me: string }) {
  const [people, setPeople] = useState<Person[]>([]);
  const [total, setTotal] = useState(0);
  const [shownQuery, setShownQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const latest = useRef(0);
  const [binding, setBinding] = useState("");
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Person | null>(null);
  const [handover, setHandover] = useState<Handover | null>(null);

  // offset 0 starts the list over (a search, a reload after a change); more appends the next page.
  // Only the latest request lands: a search or reload replaces a Load more still in flight.
  const load = useCallback(async (q = "", offset = 0) => {
    const id = ++latest.current;
    setBusy(true);
    const res = await send(pageURL("/api/admin/users", { q }, offset));
    const body = res?.ok ? await res.json().catch(() => null) : null;
    if (id !== latest.current) return;
    setBusy(false);
    if (!body) {
      setError("Could not load people. Reload the page to try again.");
      return;
    }
    setPeople((prev) => (offset ? [...prev, ...body.users] : body.users));
    setTotal(body.total);
    setShownQuery(q);
    setBinding(body.signin_binding ?? "");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function act(p: Person, action: Action, body?: object) {
    setError(null);
    const res = await send(`/api/admin/users/${encodeURIComponent(p.id)}/${action}`, {
      method: "POST",
      headers: body ? JSON_HEADERS : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res?.ok) {
      const f = await failure(res);
      setError(f.code === "reauth_required" ? REAUTH : (f.error ?? "Could not change this person"));
      // The live sign-in moved since the confirm: show the current one before anyone retries.
      if (f.code === "binding_changed") await load(query);
      return;
    }
    if (action === "reset-password") setHandover({ username: p.username, password: (await res.json()).temporary_password });
    await load(query);
  }

  function reset(p: Person) {
    if (window.confirm(`Reset ${p.username}'s password? They are signed out everywhere and their phones stop syncing until they add new app passwords.`)) {
      void act(p, "reset-password");
    }
  }

  function toggleRole(p: Person) {
    const to = p.role === "admin" ? "user" : "admin";
    const text =
      to === "admin"
        ? `Make ${p.username} an administrator? Administrators never see calendars. They are signed out everywhere.`
        : `Make ${p.username} an everyday user? They are signed out everywhere.`;
    if (window.confirm(text)) void act(p, "role", { role: to });
  }

  function toggleStatus(p: Person) {
    if (p.status !== "active") {
      void act(p, "enable");
      return;
    }
    if (window.confirm(`Disable ${p.username}? They are signed out everywhere and their phones stop syncing. Their calendars stay.`)) {
      void act(p, "disable");
    }
  }

  function reattach(p: Person) {
    const from = p.bound_to || "no recorded sign-in provider";
    if (
      window.confirm(
        `Reattach ${p.username} to the current sign-in?\n\nNow bound to: ${from}\nWill be bound to: ${binding}\n\n` +
          `Whoever this provider signs in with this account's subject gets this account and its calendars. Be sure it is the same person. ` +
          `They are enabled and signed out everywhere; their next sign-in decides their role.`,
      )
    ) {
      void act(p, "reattach", { binding });
    }
  }

  return (
    <section className="page">
      <PageHeader
        title="People"
        lede={
          <>
            Add people who sign in with a password here. People from your identity provider are read-only; change them there.
            After a sign-in provider change, you can reattach one to the current provider. Administrators manage this instance
            and never see calendars.
          </>
        }
        action={
          <button type="button" onClick={() => setAdding(true)}>
            <UserPlus size={16} aria-hidden="true" />
            Add person
          </button>
        }
      />
      <div className="toolbar">
        <form
          role="search"
          className="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            void load(query);
          }}
        >
          <div className="field">
            <label htmlFor="people-search">Search people</label>
            <input id="people-search" type="search" placeholder="Name, username or email" value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
          </div>
          <button type="submit" className="btn-secondary">
            Search
          </button>
        </form>
      </div>
      {error && <p role="alert">{error}</p>}
      {people.length === 0 && !busy ? (
        <EmptyState title={shownQuery ? `No one matches “${shownQuery}”.` : "No people yet."}>
          {shownQuery ? "Try part of a name, username or email." : "Add a person to give them a sign-in."}
        </EmptyState>
      ) : (
        <div className="table-card">
          <table className="data-table stacked">
            <thead>
              <tr>
                <th>Person</th>
                <th>Email</th>
                <th>Status</th>
                <th>Last sign-in</th>
                <th className="col-actions">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {people.map((p) => (
                <tr key={p.id}>
                  <td>
                    <span className="cell-strong">{p.display_name || p.username}</span>
                    <span className="cell-sub">
                      <span>{p.username}</span> · {p.role === "admin" ? "Administrator" : "Everyday"} <SourceBadge source={p.source} />
                    </span>
                  </td>
                  <td data-label="Email">{p.email || <span className="muted">—</span>}</td>
                  <td data-label="Status">
                    <span className="badges">
                      <span className={p.status === "active" ? "badge badge-success" : "badge"}>{p.status === "active" ? "Active" : "Disabled"}</span>
                      {p.mfa ? <span className="badge">MFA</span> : null}
                    </span>
                  </td>
                  <td data-label="Last sign-in" className="cell-nowrap">
                    {p.last_login_at ? when(p.last_login_at) : <span className="muted">Never</span>}
                  </td>
                  <td className="col-actions">
                    <div className="row-actions">
                      {p.source !== "local" && p.needs_reattach && (
                        <button type="button" className="btn-secondary btn-sm" aria-label={`Reattach ${p.username} to current sign-in`} onClick={() => reattach(p)}>
                          Reattach to current sign-in
                        </button>
                      )}
                      {p.source === "local" && (
                        <>
                          <button type="button" className="btn-secondary btn-sm" aria-label={`Edit ${p.username}`} onClick={() => setEditing(p)}>
                            Edit
                          </button>
                          <button type="button" className="btn-secondary btn-sm" aria-label={`Reset password for ${p.username}`} onClick={() => reset(p)}>
                            Reset password
                          </button>
                          {p.id !== me && (
                            <>
                              <button
                                type="button"
                                className="btn-secondary btn-sm"
                                aria-label={p.role === "admin" ? `Make ${p.username} a user` : `Make ${p.username} an admin`}
                                onClick={() => toggleRole(p)}
                              >
                                {p.role === "admin" ? "Make user" : "Make admin"}
                              </button>
                              <button
                                type="button"
                                className={p.status === "active" ? "btn-danger btn-sm" : "btn-secondary btn-sm"}
                                aria-label={p.status === "active" ? `Disable ${p.username}` : `Enable ${p.username}`}
                                onClick={() => toggleStatus(p)}
                              >
                                {p.status === "active" ? "Disable" : "Enable"}
                              </button>
                            </>
                          )}
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <LoadMore shown={people.length} total={total} busy={busy} onMore={() => void load(shownQuery, people.length)} />
      {adding && (
        <AddPerson
          onClose={() => setAdding(false)}
          onCreated={(h) => {
            setAdding(false);
            setHandover(h);
            void load(query);
          }}
        />
      )}
      {editing && (
        <EditPerson
          person={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            void load(query);
          }}
        />
      )}
      {handover && <PasswordHandover handover={handover} onClose={() => setHandover(null)} />}
    </section>
  );
}

const when = (iso: string) => new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });

function AddPerson({ onClose, onCreated }: { onClose: () => void; onCreated: (h: Handover) => void }) {
  const [form, setForm] = useState({ username: "", display_name: "", email: "", role: "user" });
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/users", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError(await refusal(res, "Could not add the person"));
      return;
    }
    onCreated({ username: form.username, password: (await res.json()).temporary_password });
  }

  return (
    <Modal title="Add person" description="They sign in with a temporary password you hand over, then choose their own." onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <div className="field">
          <label htmlFor="person-username">Username</label>
          <input id="person-username" value={form.username} onChange={set("username")} maxLength={64} autoComplete="off" required autoFocus />
        </div>
        <div className="field">
          <label htmlFor="person-name">Display name</label>
          <input id="person-name" value={form.display_name} onChange={set("display_name")} maxLength={255} />
        </div>
        <div className="field">
          <label htmlFor="person-email">Email</label>
          <input id="person-email" type="email" value={form.email} onChange={set("email")} />
        </div>
        <div className="field">
          <label htmlFor="person-role">Role</label>
          <select id="person-role" value={form.role} onChange={set("role")}>
            <option value="user">Everyday user</option>
            <option value="admin">Administrator (no calendars)</option>
          </select>
        </div>
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit">Create</button>
        </div>
      </form>
    </Modal>
  );
}

function EditPerson({ person, onClose, onSaved }: { person: Person; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState({ username: person.username, display_name: person.display_name, email: person.email });
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send(`/api/admin/users/${encodeURIComponent(person.id)}`, { method: "PATCH", headers: JSON_HEADERS, body: JSON.stringify(form) });
    if (!res?.ok) {
      setError(await refusal(res, "Could not save the changes"));
      return;
    }
    onSaved();
  }

  return (
    <Modal title={`Edit ${person.username}`} onClose={onClose}>
      <form className="form" onSubmit={submit}>
        <div className="field">
          <label htmlFor="edit-username">Username</label>
          <input id="edit-username" value={form.username} onChange={set("username")} maxLength={64} autoComplete="off" required />
        </div>
        <div className="field">
          <label htmlFor="edit-name">Display name</label>
          <input id="edit-name" value={form.display_name} onChange={set("display_name")} maxLength={255} required />
        </div>
        <div className="field">
          <label htmlFor="edit-email">Email</label>
          <input id="edit-email" type="email" value={form.email} onChange={set("email")} />
        </div>
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit">Save</button>
        </div>
      </form>
    </Modal>
  );
}

function PasswordHandover({ handover, onClose }: { handover: Handover; onClose: () => void }) {
  return (
    <Modal
      title="Temporary password"
      description={
        <>
          Give this to {handover.username} in person or over another channel you trust, not in the same message as the username.
          It is shown once: they choose their own password at first sign-in.
        </>
      }
      onClose={onClose}
    >
      <div className="secret">
        <code data-testid="temporary-password">{handover.password}</code>
        <CopyButton text={handover.password} label="Copy the temporary password" />
      </div>
      <div className="modal-actions">
        <button type="button" onClick={onClose}>
          Done
        </button>
      </div>
    </Modal>
  );
}
