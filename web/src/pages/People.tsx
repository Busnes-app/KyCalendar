import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { JSON_HEADERS, REAUTH, SourceBadge, failure, send } from "../admin";

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
  const [binding, setBinding] = useState("");
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Person | null>(null);
  const [handover, setHandover] = useState<Handover | null>(null);

  const load = useCallback(async (q = "") => {
    const res = await send(q ? `/api/admin/users?q=${encodeURIComponent(q)}` : "/api/admin/users");
    if (!res?.ok) {
      setError("Could not load people. Reload the page to try again.");
      return;
    }
    const body = await res.json();
    setPeople(body.users);
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
      <h1>People</h1>
      <p>
        Add people who sign in with a password here. People from your identity provider are read-only; change them there. After a sign-in provider change, you can reattach one to the current provider.
        Administrators manage this instance and never see calendars.
      </p>
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          void load(query);
        }}
      >
        <label>
          Search people
          <input value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
        </label>
        <button type="submit">Search</button>
      </form>
      <button type="button" onClick={() => setAdding(true)}>
        Add person
      </button>
      {error && <p role="alert">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>Username</th>
            <th>Name</th>
            <th>Email</th>
            <th>Role</th>
            <th>Status</th>
            <th>Last sign-in</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {people.map((p) => (
            <tr key={p.id}>
              <td>
                {p.username} <SourceBadge source={p.source} />
              </td>
              <td>{p.display_name}</td>
              <td>{p.email}</td>
              <td>{p.role === "admin" ? "Administrator" : "Everyday"}</td>
              <td>
                {p.status === "active" ? "Active" : "Disabled"}
                {p.mfa ? " · MFA" : ""}
              </td>
              <td>{p.last_login_at ? new Date(p.last_login_at).toLocaleString() : "Never"}</td>
              <td>
                {p.source !== "local" && p.needs_reattach && (
                  <button type="button" aria-label={`Reattach ${p.username} to current sign-in`} onClick={() => reattach(p)}>
                    Reattach to current sign-in
                  </button>
                )}
                {p.source === "local" && (
                  <>
                    <button type="button" aria-label={`Edit ${p.username}`} onClick={() => setEditing(p)}>
                      Edit
                    </button>
                    <button type="button" aria-label={`Reset password for ${p.username}`} onClick={() => reset(p)}>
                      Reset password
                    </button>
                    {p.id !== me && (
                      <>
                        <button type="button" onClick={() => toggleRole(p)}>
                          {p.role === "admin" ? `Make ${p.username} a user` : `Make ${p.username} an admin`}
                        </button>
                        <button type="button" onClick={() => toggleStatus(p)}>
                          {p.status === "active" ? `Disable ${p.username}` : `Enable ${p.username}`}
                        </button>
                      </>
                    )}
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
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

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current!;
    d.showModal();
    return () => d.close();
  }, []);
  return (
    <dialog
      ref={ref}
      aria-label={title}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2>{title}</h2>
      {children}
    </dialog>
  );
}

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
    <Modal title="Add person" onClose={onClose}>
      <form onSubmit={submit}>
        <label htmlFor="person-username">Username</label>
        <input id="person-username" value={form.username} onChange={set("username")} maxLength={64} required autoFocus />
        <label htmlFor="person-name">Display name</label>
        <input id="person-name" value={form.display_name} onChange={set("display_name")} maxLength={255} />
        <label htmlFor="person-email">Email</label>
        <input id="person-email" type="email" value={form.email} onChange={set("email")} />
        <label htmlFor="person-role">Role</label>
        <select id="person-role" value={form.role} onChange={set("role")}>
          <option value="user">Everyday user</option>
          <option value="admin">Administrator (no calendars)</option>
        </select>
        {error && <p role="alert">{error}</p>}
        <button type="submit">Create</button>
        <button type="button" className="btn-secondary" onClick={onClose}>
          Cancel
        </button>
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
      <form onSubmit={submit}>
        <label htmlFor="edit-username">Username</label>
        <input id="edit-username" value={form.username} onChange={set("username")} maxLength={64} required />
        <label htmlFor="edit-name">Display name</label>
        <input id="edit-name" value={form.display_name} onChange={set("display_name")} maxLength={255} required />
        <label htmlFor="edit-email">Email</label>
        <input id="edit-email" type="email" value={form.email} onChange={set("email")} />
        {error && <p role="alert">{error}</p>}
        <button type="submit">Save</button>
        <button type="button" className="btn-secondary" onClick={onClose}>
          Cancel
        </button>
      </form>
    </Modal>
  );
}

function PasswordHandover({ handover, onClose }: { handover: Handover; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <Modal title="Temporary password" onClose={onClose}>
      <p>
        Give this to {handover.username} in person or over another channel you trust, not in the same message as the
        username. It is shown once: they choose their own password at first sign-in.
      </p>
      <p>
        <code data-testid="temporary-password">{handover.password}</code>
      </p>
      <button
        type="button"
        onClick={() =>
          void navigator.clipboard?.writeText(handover.password).then(
            () => setCopied(true),
            () => setCopied(false),
          )
        }
      >
        {copied ? "Copied" : "Copy"}
      </button>
      <button type="button" onClick={onClose}>
        Done
      </button>
    </Modal>
  );
}
