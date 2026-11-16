import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { JSON_HEADERS, failure, send } from "../admin";
import { CopyButton, EmptyState, Modal, PageHeader } from "../components/ui";

interface Ref {
  id: string;
  display_name: string;
  username?: string;
}

export interface Resource {
  id: string;
  address: string;
  kind: "room" | "equipment";
  name: string;
  location: string;
  capacity: number;
  tags: string[];
  audience: "everyone" | "groups";
  groups: string[];
  approval: "auto" | "approvers";
  approvers: string[];
  max_minutes: number;
  horizon_days: number;
  enabled: boolean;
  group_refs: Ref[];
  approver_refs: Ref[];
}

type Draft = Omit<Resource, "id" | "address" | "group_refs"> & { id?: string };

const BLANK: Draft = {
  kind: "room", name: "", location: "", capacity: 0, tags: [], audience: "everyone", groups: [],
  approval: "auto", approvers: [], max_minutes: 1440, horizon_days: 180, enabled: true, approver_refs: [],
};

export const KIND_LABEL = { room: "Room", equipment: "Equipment" } as const;

function hours(min: number): string {
  return min % 60 === 0 ? `${min / 60} h` : `${min} min`;
}

export default function Resources() {
  const [resources, setResources] = useState<Resource[]>([]);
  const [groups, setGroups] = useState<Ref[]>([]);
  const [editing, setEditing] = useState<Draft | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [rs, gs] = await Promise.all([send("/api/admin/resources"), send("/api/admin/groups?limit=200")]);
    if (!rs?.ok || !gs?.ok) {
      setError("Could not load rooms and equipment. Reload the page to try again.");
      return;
    }
    setResources((await rs.json()).resources);
    setGroups((await gs.json()).groups);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <section>
      <PageHeader
        title="Rooms and equipment"
        lede="People add a room or equipment to an event, and KyCalendar answers for it: accepted when it is free and they may book it, or sent to its approvers."
        action={
          <button type="button" className="ky-page-action" onClick={() => setEditing({ ...BLANK })}>
            <Plus aria-hidden="true" />
            New room or equipment
          </button>
        }
      />
      {editing && (
        <ResourceEditor
          draft={editing}
          groups={groups}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null);
            await load();
          }}
        />
      )}
      {error && <p role="alert">{error}</p>}
      {resources.length === 0 ? (
        <EmptyState title="No rooms or equipment yet.">Create one with New room or equipment. People can book it from the event editor straight away.</EmptyState>
      ) : (
        resources.map((r) => (
          <article key={r.id} className="card" aria-labelledby={`res-${r.id}`}>
            <div className="card-header">
              <div>
                <h2 className="card-title" id={`res-${r.id}`}>
                  {r.name} <span className="badge">{KIND_LABEL[r.kind]}</span>
                  {!r.enabled && <span className="badge">Disabled</span>}
                </h2>
                <p className="card-meta">
                  {[r.location, r.capacity > 0 ? `${r.capacity} people` : "", r.tags.join(", ")].filter(Boolean).join(" · ") || "No details"}
                </p>
              </div>
              <button type="button" className="btn-secondary btn-sm" onClick={() => setEditing({ ...r })}>Edit</button>
            </div>
            <dl className="facts card-body">
              <dt>Who may book</dt>
              <dd>{r.audience === "everyone" ? "Everyone" : r.group_refs.map((g) => g.display_name).join(", ") || "No group (nobody)"}</dd>
              <dt>Requests</dt>
              <dd>{r.approval === "auto" ? "Accepted automatically when free" : `Approved by ${r.approver_refs.map((p) => p.display_name || p.username).join(", ")}`}</dd>
              <dt>Limits</dt>
              <dd>{`Up to ${hours(r.max_minutes)}, at most ${r.horizon_days} days ahead`}</dd>
              <dt>Calendar address</dt>
              <dd>
                <code>{r.address}</code> <CopyButton text={r.address} />
              </dd>
            </dl>
          </article>
        ))
      )}
    </section>
  );
}

interface EditorProps {
  draft: Draft;
  groups: Ref[];
  onClose: () => void;
  onSaved: () => Promise<void>;
}

function ResourceEditor({ draft, groups, onClose, onSaved }: EditorProps) {
  const [d, setD] = useState<Draft>(draft);
  const [tags, setTags] = useState(draft.tags.join(", "));
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Ref[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));

  async function search(e: React.FormEvent) {
    e.preventDefault();
    const res = await send(`/api/admin/users?q=${encodeURIComponent(query)}`);
    if (!res?.ok) {
      setError("Could not search people.");
      return;
    }
    const people: (Ref & { role: string; status: string })[] = (await res.json()).users;
    setFound(people.filter((p) => p.role === "user" && p.status === "active" && !d.approvers.includes(p.id)));
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const body = {
      kind: d.kind, name: d.name, location: d.location, capacity: d.capacity,
      tags: tags.split(",").map((t) => t.trim()).filter(Boolean),
      audience: d.audience, groups: d.groups, approval: d.approval, approvers: d.approvers,
      max_minutes: d.max_minutes, horizon_days: d.horizon_days, enabled: d.enabled,
    };
    const res = await send(d.id ? `/api/admin/resources/${encodeURIComponent(d.id)}` : "/api/admin/resources", {
      method: d.id ? "PUT" : "POST", headers: JSON_HEADERS, body: JSON.stringify(body),
    });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "Could not save. Nothing changed.");
      return;
    }
    await onSaved();
  }

  return (
    <Modal wide title={d.id ? `Edit ${draft.name}` : "New room or equipment"} description="Disabling keeps its bookings; nobody can book it until you enable it again." onClose={onClose}>
      <form className="form" onSubmit={save}>
        <div className="field">
          <label htmlFor="res-kind">Kind</label>
          <select id="res-kind" value={d.kind} onChange={(e) => set("kind", e.target.value as Draft["kind"])}>
            <option value="room">Room</option>
            <option value="equipment">Equipment</option>
          </select>
        </div>
        <div className="field">
          <label htmlFor="res-name">Name</label>
          <input id="res-name" value={d.name} onChange={(e) => set("name", e.target.value)} maxLength={100} placeholder="For example, Fjord" required autoFocus />
        </div>
        <div className="field">
          <label htmlFor="res-location">Location</label>
          <input id="res-location" value={d.location} onChange={(e) => set("location", e.target.value)} maxLength={200} placeholder="For example, Floor 2, east wing" />
        </div>
        <div className="field field-narrow">
          <label htmlFor="res-capacity">Capacity</label>
          <input id="res-capacity" type="number" min={0} max={10000} value={d.capacity} onChange={(e) => set("capacity", Number(e.target.value))} />
          <p className="field-hint">People it seats; 0 leaves it unstated.</p>
        </div>
        <div className="field">
          <label htmlFor="res-tags">Equipment tags</label>
          <input id="res-tags" value={tags} onChange={(e) => setTags(e.target.value)} placeholder="screen, video, whiteboard" />
          <p className="field-hint">Separate tags with commas. People filter rooms by them.</p>
        </div>
        <fieldset>
          <legend>Who may book</legend>
          <label className="choice">
            <input type="radio" name="res-audience" checked={d.audience === "everyone"} onChange={() => set("audience", "everyone")} /> Everyone
          </label>
          <label className="choice">
            <input type="radio" name="res-audience" checked={d.audience === "groups"} onChange={() => set("audience", "groups")} /> Only these groups
          </label>
          {d.audience === "groups" && (groups.length === 0 ? (
            <p className="field-hint">No groups yet. Create one under Groups first.</p>
          ) : (
            groups.map((g) => (
              <label key={g.id} className="choice">
                <input
                  type="checkbox"
                  checked={d.groups.includes(g.id)}
                  onChange={(e) => set("groups", e.target.checked ? [...d.groups, g.id] : d.groups.filter((x) => x !== g.id))}
                />{" "}
                {g.display_name}
              </label>
            ))
          ))}
        </fieldset>
        <fieldset>
          <legend>Requests</legend>
          <label className="choice">
            <input type="radio" name="res-approval" checked={d.approval === "auto"} onChange={() => set("approval", "auto")} /> Accept automatically when free
          </label>
          <label className="choice">
            <input type="radio" name="res-approval" checked={d.approval === "approvers"} onChange={() => set("approval", "approvers")} /> Approvers confirm each request
          </label>
          {d.approval === "approvers" && (
            <>
              <ul className="record-list" aria-label="Approvers">
                {d.approver_refs.map((p) => (
                  <li key={p.id}>
                    <span className="record-main cell-strong">{p.display_name || p.username}</span>
                    <span className="record-actions">
                      <button
                        type="button"
                        className="btn-ghost btn-danger btn-sm"
                        aria-label={`Remove ${p.username}`}
                        onClick={() => setD((x) => ({ ...x, approvers: x.approvers.filter((i) => i !== p.id), approver_refs: x.approver_refs.filter((r) => r.id !== p.id) }))}
                      >
                        Remove
                      </button>
                    </span>
                  </li>
                ))}
              </ul>
              <div className="inline-form">
                <div className="field">
                  <label htmlFor="res-find">Find an approver</label>
                  <input id="res-find" type="search" placeholder="Name or username" value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
                </div>
                <button type="button" className="btn-secondary" onClick={(e) => void search(e)}>Search</button>
              </div>
              {found && (found.length === 0 ? (
                <p className="field-hint">Only active everyday people can approve.</p>
              ) : (
                <ul className="record-list" aria-label="Search results">
                  {found.map((p) => (
                    <li key={p.id}>
                      <span className="record-main cell-strong">{p.display_name || p.username}</span>
                      <span className="record-actions">
                        <button
                          type="button"
                          className="btn-secondary btn-sm"
                          aria-label={`Add ${p.username}`}
                          onClick={() => {
                            setD((x) => ({ ...x, approvers: [...x.approvers, p.id], approver_refs: [...x.approver_refs, p] }));
                            setFound(null);
                            setQuery("");
                          }}
                        >
                          Add
                        </button>
                      </span>
                    </li>
                  ))}
                </ul>
              ))}
            </>
          )}
        </fieldset>
        <div className="field field-narrow">
          <label htmlFor="res-max">Longest booking (minutes)</label>
          <input id="res-max" type="number" min={5} max={20160} value={d.max_minutes} onChange={(e) => set("max_minutes", Number(e.target.value))} />
        </div>
        <div className="field field-narrow">
          <label htmlFor="res-horizon">Bookable days ahead</label>
          <input id="res-horizon" type="number" min={1} max={730} value={d.horizon_days} onChange={(e) => set("horizon_days", Number(e.target.value))} />
          <p className="field-hint">A repeating event must end within this many days.</p>
        </div>
        <label className="check-row">
          <input type="checkbox" checked={d.enabled} onChange={(e) => set("enabled", e.target.checked)} /> Can be booked
        </label>
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit">Save</button>
        </div>
      </form>
    </Modal>
  );
}
