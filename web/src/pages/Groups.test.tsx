import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Groups from "./Groups";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

type Handler = (init?: RequestInit) => unknown;

function mockFetch(handlers: Record<string, Handler>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : 200 });
  });
}

const crew = { id: "grp_1", display_name: "Crew", source: "local", member_count: 0, calendar_count: 2 };
const synced = { id: "grp_2", display_name: "Synced", source: "scim", member_count: 1, calendar_count: 0 };
const list = (...groups: unknown[]) => ({ groups, total: groups.length });

describe("Groups", () => {
  it("creates a local group", async () => {
    let posted: unknown;
    mockFetch({
      "GET /api/admin/groups": () => list(),
      "POST /api/admin/groups": (init) => {
        posted = JSON.parse(String(init?.body));
        return crew;
      },
    });
    render(<Groups />);
    fireEvent.change(screen.getByLabelText("Group name"), { target: { value: "Crew" } });
    fireEvent.click(screen.getByRole("button", { name: "Create group" }));
    await waitFor(() => expect(posted).toEqual({ display_name: "Crew" }));
  });

  it("shows synced groups read-only", async () => {
    mockFetch({
      "GET /api/admin/groups": () => list(synced),
      "GET /api/admin/groups/grp_2": () => ({ ...synced, members: [{ id: "u1", username: "bob", display_name: "Bob", source: "scim" }] }),
    });
    render(<Groups />);
    const card = await screen.findByRole("article", { name: "Synced" });
    expect(within(card).getByText("Managed by SCIM")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "Rename" })).toBeNull();
    expect(within(card).queryByRole("button", { name: "Delete group" })).toBeNull();
    fireEvent.click(within(card).getByRole("button", { name: "Members" }));
    expect(await within(card).findByText("Bob")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: /Remove/ })).toBeNull();
    expect(within(card).queryByLabelText("Find a person")).toBeNull();
  });

  it("names the calendars a delete affects and asks for a fresh sign-in", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/groups": () => list(crew),
      "DELETE /api/admin/groups/grp_1": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<Groups />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete group" }));
    expect(confirm.mock.calls[0][0]).toMatch(/2 group calendars lose this group's access/);
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("adds only active everyday people found by search", async () => {
    const put = vi.fn(() => null);
    mockFetch({
      "GET /api/admin/groups": () => list(crew),
      "GET /api/admin/groups/grp_1": () => ({ ...crew, members: [] }),
      "GET /api/admin/users?q=wal": () => ({
        users: [
          { id: "u1", username: "walter", display_name: "Walter", role: "user", status: "active" },
          { id: "u2", username: "walt-admin", display_name: "Walt", role: "admin", status: "active" },
          { id: "u3", username: "wally", display_name: "Wally", role: "user", status: "inactive" },
        ],
        total: 3,
      }),
      "PUT /api/admin/groups/grp_1/members/u1": put,
    });
    render(<Groups />);
    const card = await screen.findByRole("article", { name: "Crew" });
    fireEvent.click(within(card).getByRole("button", { name: "Members" }));
    fireEvent.change(await within(card).findByLabelText("Find a person"), { target: { value: "wal" } });
    fireEvent.click(within(card).getByRole("button", { name: "Search" }));
    const add = await within(card).findByRole("button", { name: "Add walter" });
    expect(within(card).queryByRole("button", { name: "Add walt-admin" })).toBeNull();
    expect(within(card).queryByRole("button", { name: "Add wally" })).toBeNull();
    fireEvent.click(add);
    await waitFor(() => expect(put).toHaveBeenCalled());
  });

  it("flags a local group whose name SCIM could not create", async () => {
    mockFetch({ "GET /api/admin/groups": () => list({ ...crew, scim_conflict: true }) });
    render(<Groups />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/cannot create it/);
  });

  it("alerts when the list cannot load", async () => {
    mockFetch({ "GET /api/admin/groups": () => new Response("{}", { status: 500 }) });
    render(<Groups />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });

  it("pages past 200 groups with Load more", async () => {
    const many = (from: number, n: number) => Array.from({ length: n }, (_, i) => ({ ...crew, id: `grp_${from + i}`, display_name: `G${from + i}` }));
    mockFetch({
      "GET /api/admin/groups": () => ({ groups: many(0, 200), total: 201 }),
      "GET /api/admin/groups?offset=200": () => ({ groups: many(200, 1), total: 201 }),
    });
    render(<Groups />);
    expect(await screen.findByText("Showing 200 of 201")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("G200")).toBeTruthy();
    expect(screen.queryByText(/^Showing/)).toBeNull();
    expect(screen.getByText("G0")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });
});

