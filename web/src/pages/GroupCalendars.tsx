import { useCallback, useEffect, useState } from "react";
import { JSON_HEADERS, send } from "../admin";
import { Plus } from "lucide-react";
import { HOLDS, holdsOf, type Holds } from "../calendarApi";
import { calendarColors, nextColor } from "../calendarColors";
import { EmptyState, Modal, PageHeader } from "../components/ui";

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
const ROLE_LABEL: Record<string, string> = { reader: "Reader", editor: "Editor", manager: "Manager" };
// Group calendars start further along the palette than personal ones, so the two rarely meet.
const GROUP_PALETTE_START = 2;

async function errorText(res: Response | null, fallback: string): Promise<string> {
  return (await res?.json().catch(() => null))?.error ?? fallback;
}

export default function GroupCalendars() {
  const [calendars, setCalendars] = useState<GroupCalendar[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [name, setName] = useState("");
  const [holds, setHolds] = useState<Holds>("events");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
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
    setCreateError(null);
    const res = await send("/api/admin/calendars", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify({ name, components: HOLDS[holds].components, color: nextColor(calendars.map((c) => c.color), GROUP_PALETTE_START) }) });
    if (!res?.ok) {
      setCreateError(await errorText(res, "Could not create the calendar"));
      return;
    }
    closeCreate();
    await load();
  }

  function closeCreate() {
    setCreating(false);
    setCreateError(null);
    setName("");
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

  const colors = calendarColors(calendars);

  return (
    <section>
      <PageHeader
        title="Group calendars"
        lede="Group calendars belong to the organisation: readers see events, editors change them, managers also rename the calendar and choose who has access."
        action={
          <button type="button" className="ky-page-action" onClick={() => setCreating(true)}>
            <Plus aria-hidden="true" />
            New group calendar
          </button>
        }
      />
      {creating && (
        <Modal title="New group calendar" description="Administrators manage access but never see events." onClose={closeCreate}>
          <form className="form" onSubmit={create}>
            <div className="field">
              <label htmlFor="gcal-name">Calendar name</label>
              <input id="gcal-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={255} placeholder="For example, Studio" required autoFocus />
            </div>
            <div className="field">
              <label htmlFor="gcal-holds">Holds</label>
              <select id="gcal-holds" value={holds} onChange={(e) => setHolds(holdsOf(e.target.value))}>
                {Object.entries(HOLDS).map(([k, h]) => <option key={k} value={k}>{h.label}</option>)}
              </select>
            </div>
            {createError && <p role="alert">{createError}</p>}
            <div className="modal-actions">
              <button type="button" className="btn-secondary" onClick={closeCreate}>
                Cancel
              </button>
              <button type="submit">Create</button>
            </div>
          </form>
        </Modal>
      )}
      {error && <p role="alert">{error}</p>}
      {calendars.length === 0 ? (
        <EmptyState title="No group calendars yet.">Create one with New group calendar, then give a group access so its members see it.</EmptyState>
      ) : (
        calendars.map((cal) => (
          <CalendarCard key={cal.id} cal={cal} color={colors.get(cal.id)!} groups={groups} onSet={setGrant} onRemove={removeGrant} onDelete={remove} />
        ))
      )}
    </section>
  );
}

interface CardProps {
  cal: GroupCalendar;
  color: string;
  groups: Group[];
  onSet: (cal: string, group: string, role: string) => Promise<void>;
  onRemove: (cal: string, group: string) => Promise<void>;
  onDelete: (cal: GroupCalendar) => Promise<void>;
}

function CalendarCard({ cal, color, groups, onSet, onRemove, onDelete }: CardProps) {
  const [group, setGroup] = useState("");
  const [role, setRole] = useState("reader");
  const available = groups.filter((g) => !cal.grants.some((x) => x.group_id === g.id));

  return (
    <article className="card" aria-labelledby={`cal-${cal.id}`}>
      <div className="card-header">
        <div>
          <h2 className="card-title" id={`cal-${cal.id}`}>
            <span className="kc-swatch" style={{ background: color }} aria-hidden="true" />
            {cal.name}
          </h2>
          <p className="card-meta">
            {cal.grants.length === 0 ? "No access yet" : cal.grants.length === 1 ? "1 group has access" : `${cal.grants.length} groups have access`}
          </p>
        </div>
        <button type="button" className="btn-danger btn-sm" onClick={() => void onDelete(cal)}>Delete calendar</button>
      </div>
      <div className="card-body">
        {cal.grants.length === 0 ? (
          <p className="field-hint">Members cannot see this calendar until you add a group.</p>
        ) : (
          <ul className="record-list" aria-label={`Access to ${cal.name}`}>
            {cal.grants.map((g) => (
              <li key={g.group_id}>
                <span className="record-main cell-strong">{g.group_name}</span>
                <span className="record-actions">
                  <select className="select-compact" aria-label={`Role for ${g.group_name}`} value={g.role} onChange={(e) => void onSet(cal.id, g.group_id, e.target.value)}>
                    {ROLES.map((r) => (
                      <option key={r} value={r}>{ROLE_LABEL[r]}</option>
                    ))}
                  </select>
                  <button type="button" className="btn-ghost btn-danger btn-sm" aria-label={`Remove ${g.group_name}`} onClick={() => void onRemove(cal.id, g.group_id)}>
                    Remove
                  </button>
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <form
        className="inline-form card-footer group-calendar-grant"
        onSubmit={(e) => {
          e.preventDefault();
          if (!group) return;
          void onSet(cal.id, group, role);
          setGroup("");
        }}
      >
        {/* htmlFor, not nesting: a nested select's options would join the label's name. */}
        <div className="field">
          <label htmlFor={`group-${cal.id}`}>Group</label>
          <select id={`group-${cal.id}`} value={group} onChange={(e) => setGroup(e.target.value)} required disabled={available.length === 0}>
            <option value="">{groups.length === 0 ? "No groups available" : available.length === 0 ? "Every group has access" : "Choose a group"}</option>
            {available.map((g) => (
              <option key={g.id} value={g.id}>{g.display_name}</option>
            ))}
          </select>
        </div>
        <div className="field field-narrow">
          <label htmlFor={`role-${cal.id}`}>Role</label>
          <select id={`role-${cal.id}`} value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>{ROLE_LABEL[r]}</option>
            ))}
          </select>
        </div>
        <button type="submit" className="btn-secondary" disabled={!group || available.length === 0}>Add group</button>
      </form>
    </article>
  );
}
