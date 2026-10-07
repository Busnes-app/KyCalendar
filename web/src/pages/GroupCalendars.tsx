import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";

interface Grant {
  group_id: string;
  group_name: string;
  role: string;
}

interface GroupCalendar {
  id: string;
  name: string;
  color: string;
  description: string;
  grants: Grant[];
}

interface Group {
  id: string;
  display_name: string;
}

const ROLES = ["reader", "editor", "manager"];
const JSON_HEADERS = { "Content-Type": "application/json" };

// Resolves to null on a network error so every caller handles one failure path.
const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

async function errorText(res: Response | null, fallback: string): Promise<string> {
  return (await res?.json().catch(() => null))?.error ?? fallback;
}

export default function GroupCalendars() {
  const [calendars, setCalendars] = useState<GroupCalendar[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [cals, grps] = await Promise.all([send("/api/admin/calendars"), send("/api/admin/groups")]);
    if (!cals?.ok || !grps?.ok) {
      setError("Could not load group calendars. Reload the page to try again.");
      return;
    }
    setCalendars(await cals.json());
    setGroups((await grps.json()).groups);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/calendars", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify({ name }) });
    if (!res?.ok) {
      setError(await errorText(res, "Could not create the calendar"));
      return;
    }
    setName("");
    await load();
  }

  async function setGrant(cal: string, group: string, role: string) {
    setError(null);
    const url = `/api/calendars/${encodeURIComponent(cal)}/grants/${encodeURIComponent(group)}`;
    const res = await send(url, { method: "PUT", headers: JSON_HEADERS, body: JSON.stringify({ role }) });
    if (!res?.ok) {
      setError(await errorText(res, "Could not change access"));
      return;
    }
    await load();
  }

  async function removeGrant(cal: string, group: string) {
    setError(null);
    const res = await send(`/api/calendars/${encodeURIComponent(cal)}/grants/${encodeURIComponent(group)}`, { method: "DELETE" });
    if (!res?.ok) {
      setError("Could not remove access. The group still has it; try again.");
      return;
    }
    await load();
  }

  async function remove(cal: GroupCalendar) {
    if (!window.confirm(`Delete ${cal.name} and every event in it? This cannot be undone.`)) return;
    setError(null);
    const res = await send(`/api/admin/calendars/${encodeURIComponent(cal.id)}`, { method: "DELETE" });
    if (res?.status === 403 && (await res.json().catch(() => null))?.code === "reauth_required") {
      setError("Sign out and sign in again, then delete the calendar within 10 minutes.");
      return;
    }
    if (!res?.ok) {
      setError("Could not delete the calendar. It still exists.");
      return;
    }
    await load();
  }

  return (
    <section className="page">
      <h1>Group calendars</h1>
      <p>
        Group calendars belong to the organisation. Readers see events, editors change them, and managers also rename the
        calendar and change who has access. Administrators manage access but never see events.
      </p>
      <form onSubmit={create}>
        <label>
          Calendar name
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={255} required />
        </label>
        <button type="submit">Create</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {calendars.length === 0 ? (
        <p>No group calendars yet.</p>
      ) : (
        calendars.map((cal) => (
          <CalendarCard key={cal.id} cal={cal} groups={groups} onSet={setGrant} onRemove={removeGrant} onDelete={remove} />
        ))
      )}
    </section>
  );
}

interface CardProps {
  cal: GroupCalendar;
  groups: Group[];
  onSet: (cal: string, group: string, role: string) => Promise<void>;
  onRemove: (cal: string, group: string) => Promise<void>;
  onDelete: (cal: GroupCalendar) => Promise<void>;
}

function CalendarCard({ cal, groups, onSet, onRemove, onDelete }: CardProps) {
  const [group, setGroup] = useState("");
  const [role, setRole] = useState("reader");
  const available = groups.filter((g) => !cal.grants.some((x) => x.group_id === g.id));

  return (
    <article aria-labelledby={`cal-${cal.id}`}>
      <h2 id={`cal-${cal.id}`}>{cal.name}</h2>
      {cal.grants.length === 0 ? (
        <p>No group has access. Members cannot see this calendar until you add a group.</p>
      ) : (
        <ul>
          {cal.grants.map((g) => (
            <li key={g.group_id}>
              <span>{g.group_name}</span>{" "}
              <select aria-label={`Role for ${g.group_name}`} value={g.role} onChange={(e) => void onSet(cal.id, g.group_id, e.target.value)}>
                {ROLES.map((r) => (
                  <option key={r} value={r}>{r}</option>
                ))}
              </select>{" "}
              <button type="button" aria-label={`Remove ${g.group_name}`} onClick={() => void onRemove(cal.id, g.group_id)}>
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!group) return;
          void onSet(cal.id, group, role);
          setGroup("");
        }}
      >
        {/* htmlFor, not nesting: a nested select's options would join the label's name. */}
        <label htmlFor={`group-${cal.id}`}>Group</label>
        <select id={`group-${cal.id}`} value={group} onChange={(e) => setGroup(e.target.value)} required>
          <option value="">Choose a group</option>
          {available.map((g) => (
            <option key={g.id} value={g.id}>{g.display_name}</option>
          ))}
        </select>
        <label htmlFor={`role-${cal.id}`}>Role</label>
        <select id={`role-${cal.id}`} value={role} onChange={(e) => setRole(e.target.value)}>
          {ROLES.map((r) => (
            <option key={r} value={r}>{r}</option>
          ))}
        </select>
        <button type="submit">Add group</button>
      </form>
      <button type="button" onClick={() => void onDelete(cal)}>Delete calendar</button>
    </article>
  );
}
