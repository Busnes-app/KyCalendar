import { useCallback, useEffect, useRef, useState } from "react";
import { JSON_HEADERS, LoadMore, REAUTH, SourceBadge, failure, pageURL, send } from "../admin";

interface Group {
  id: string;
  display_name: string;
  source: string;
  member_count: number;
  calendar_count: number;
  scim_conflict?: boolean;
}

interface Member {
  id: string;
  username: string;
  display_name: string;
  source: string;
}

interface Person {
  id: string;
  username: string;
  display_name: string;
  role: string;
  status: string;
}

function calendarsLosing(n: number): string {
  return n === 1 ? "1 group calendar loses" : `${n} group calendars lose`;
}

export default function Groups() {
  const [groups, setGroups] = useState<Group[]>([]);
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState(false);
  const latest = useRef(0);
  const [name, setName] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // offset 0 starts the list over (after a change); more appends the next page.
  // Only the latest request lands: a reload replaces a Load more still in flight.
  const load = useCallback(async (offset = 0) => {
    const id = ++latest.current;
    setBusy(true);
    const res = await send(pageURL("/api/admin/groups", {}, offset));
    const body = res?.ok ? await res.json().catch(() => null) : null;
    if (id !== latest.current) return;
    setBusy(false);
    if (!body) {
      setError("Could not load groups. Reload the page to try again.");
      return;
    }
    setGroups((prev) => (offset ? [...prev, ...body.groups] : body.groups));
    setTotal(body.total);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const res = await send("/api/admin/groups", { method: "POST", headers: JSON_HEADERS, body: JSON.stringify({ display_name: name }) });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "Could not create the group");
      return;
    }
    setName("");
    await load();
  }

  async function rename(g: Group, next: string) {
    setError(null);
    const res = await send(`/api/admin/groups/${encodeURIComponent(g.id)}`, {
      method: "PATCH",
      headers: JSON_HEADERS,
      body: JSON.stringify({ display_name: next }),
    });
    if (!res?.ok) {
      setError((await failure(res)).error ?? "Could not rename the group");
      return;
    }
    await load();
  }

  async function remove(g: Group) {
    if (!window.confirm(`Delete ${g.display_name}? ${calendarsLosing(g.calendar_count)} this group's access. The calendars stay.`)) return;
    setError(null);
    const res = await send(`/api/admin/groups/${encodeURIComponent(g.id)}`, { method: "DELETE" });
    if (res?.status === 403 && (await failure(res)).code === "reauth_required") {
      setError(REAUTH);
      return;
    }
    if (!res?.ok) {
      setError("Could not delete the group. It still exists.");
      return;
    }
    if (open === g.id) setOpen(null);
    await load();
  }

  return (
    <section className="page">
      <h1>Groups</h1>
      <p>
        Groups give people access to group calendars. Groups from your identity provider are read-only here; change them
        there.
      </p>
      <form onSubmit={create}>
        <label>
          Group name
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={255} required />
        </label>
        <button type="submit">Create group</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {groups.length === 0 ? (
        <p>No groups yet.</p>
      ) : (
        groups.map((g) => (
          <GroupCard
            key={g.id}
            group={g}
            open={open === g.id}
            onToggle={() => setOpen(open === g.id ? null : g.id)}
            onRename={rename}
            onDelete={remove}
            onChanged={load}
            onError={setError}
          />
        ))
      )}
      <LoadMore shown={groups.length} total={total} busy={busy} onMore={() => void load(groups.length)} />
    </section>
  );
}

interface CardProps {
  group: Group;
  open: boolean;
  onToggle: () => void;
  onRename: (g: Group, next: string) => Promise<void>;
  onDelete: (g: Group) => Promise<void>;
  onChanged: () => Promise<void>;
  onError: (message: string) => void;
}

function GroupCard({ group, open, onToggle, onRename, onDelete, onChanged, onError }: CardProps) {
  const local = group.source === "local";
  const [draft, setDraft] = useState(group.display_name);

  return (
    <article aria-labelledby={`group-${group.id}`}>
      <h2 id={`group-${group.id}`}>{group.display_name}</h2>
      <SourceBadge source={group.source} />
      <p>
        {group.member_count} {group.member_count === 1 ? "member" : "members"} · {group.calendar_count} group{" "}
        {group.calendar_count === 1 ? "calendar" : "calendars"}
      </p>
      {group.scim_conflict && (
        <p role="alert">
          Your identity provider has a group with this name and cannot create it. Rename this group to let it through.
        </p>
      )}
      {local && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void onRename(group, draft);
          }}
        >
          <label htmlFor={`rename-${group.id}`}>New name</label>
          <input id={`rename-${group.id}`} value={draft} onChange={(e) => setDraft(e.target.value)} maxLength={255} required />
          <button type="submit">Rename</button>
        </form>
      )}
      <button type="button" aria-expanded={open} onClick={onToggle}>
        Members
      </button>
      {local && (
        <button type="button" onClick={() => void onDelete(group)}>
          Delete group
        </button>
      )}
      {open && <MembersPanel group={group} onChanged={onChanged} onError={onError} />}
    </article>
  );
}

function MembersPanel({ group, onChanged, onError }: { group: Group; onChanged: () => Promise<void>; onError: (m: string) => void }) {
  const local = group.source === "local";
  const [members, setMembers] = useState<Member[]>([]);
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Person[] | null>(null);
  const base = `/api/admin/groups/${encodeURIComponent(group.id)}`;

  const load = useCallback(async () => {
    const res = await send(base);
    if (!res?.ok) {
      onError("Could not load the members.");
      return;
    }
    setMembers((await res.json()).members);
  }, [base, onError]);

  useEffect(() => {
    void load();
  }, [load]);

  async function search(e: React.FormEvent) {
    e.preventDefault();
    const res = await send(`/api/admin/users?q=${encodeURIComponent(query)}`);
    if (!res?.ok) {
      onError("Could not search people.");
      return;
    }
    const people: Person[] = (await res.json()).users;
    // Administrators never see calendars and inactive people cannot sign in: neither can join.
    setFound(people.filter((p) => p.role === "user" && p.status === "active" && !members.some((m) => m.id === p.id)));
  }

  async function change(method: "PUT" | "DELETE", userId: string, failed: string) {
    const res = await send(`${base}/members/${encodeURIComponent(userId)}`, { method });
    if (!res?.ok) {
      onError((await failure(res)).error ?? failed);
      return;
    }
    setFound(null);
    await load();
    await onChanged();
  }

  return (
    <div>
      {members.length === 0 ? (
        <p>No members.</p>
      ) : (
        <ul aria-label={`Members of ${group.display_name}`}>
          {members.map((m) => (
            <li key={m.id}>
              <span>{m.display_name || m.username}</span> <small>{m.username}</small>{" "}
              {local && (
                <button type="button" aria-label={`Remove ${m.username}`} onClick={() => void change("DELETE", m.id, "Could not remove the member")}>
                  Remove
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {local && (
        <form onSubmit={search}>
          <label htmlFor={`find-${group.id}`}>Find a person</label>
          <input id={`find-${group.id}`} value={query} onChange={(e) => setQuery(e.target.value)} maxLength={255} />
          <button type="submit">Search</button>
        </form>
      )}
      {found && (found.length === 0 ? (
        <p>No one else can join: only active everyday people can be members.</p>
      ) : (
        <ul aria-label="Search results">
          {found.map((p) => (
            <li key={p.id}>
              <span>{p.display_name || p.username}</span>{" "}
              <button type="button" aria-label={`Add ${p.username}`} onClick={() => void change("PUT", p.id, "Could not add the member")}>
                Add
              </button>
            </li>
          ))}
        </ul>
      ))}
    </div>
  );
}
