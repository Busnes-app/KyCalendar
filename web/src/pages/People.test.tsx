import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import People from "./People";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = await handler(init);
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const person = (over: Record<string, unknown>) => ({
  id: "u1", username: "ann", display_name: "Ann", email: "", role: "user", status: "active", source: "local",
  mfa: false, must_change_password: false, last_login_at: null, ...over,
});
const users = (...list: unknown[]) => ({ users: list, total: list.length, signin_binding: "kyidentity https://b.example" });

describe("People", () => {
  it("shows a new person's temporary password once", async () => {
    let posted: unknown;
    mockFetch({
      "GET /api/admin/users": () => users(),
      "POST /api/admin/users": (init) => {
        posted = JSON.parse(String(init?.body));
        return { user: person({}), temporary_password: "Tmp-Once-Only-1234" };
      },
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add person" }));
    const add = screen.getByRole("dialog", { name: "Add person" });
    fireEvent.change(within(add).getByLabelText("Username"), { target: { value: "ann" } });
    fireEvent.click(within(add).getByRole("button", { name: "Create" }));
    const handover = await screen.findByRole("dialog", { name: "Temporary password" });
    expect(within(handover).getByTestId("temporary-password").textContent).toBe("Tmp-Once-Only-1234");
    expect(posted).toEqual({ username: "ann", display_name: "", email: "", role: "user" });
    fireEvent.click(within(handover).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByText("Tmp-Once-Only-1234")).toBeNull());
  });

  it("shows synced people with a badge and no actions", async () => {
    mockFetch({ "GET /api/admin/users": () => users(person({ id: "u2", username: "sam", source: "kysignon" })) });
    render(<People me="u0" />);
    const row = (await screen.findByText("Managed by KyIdentity")).closest("tr")!;
    expect(within(row).queryAllByRole("button")).toHaveLength(0);
  });

  it("offers no demote or disable on your own row", async () => {
    mockFetch({ "GET /api/admin/users": () => users(person({ id: "me", username: "root", role: "admin" })) });
    render(<People me="me" />);
    await screen.findByRole("button", { name: "Edit root" });
    expect(screen.queryByRole("button", { name: /Make root/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Disable root/ })).toBeNull();
  });

  it("asks for a fresh sign-in when a reset needs step-up", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/users": () => users(person({})),
      "POST /api/admin/users/u1/reset-password": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reset password for ann" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("explains why the last local admin cannot be disabled", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/users": () => users(person({ role: "admin" })),
      "POST /api/admin/users/u1/disable": () =>
        new Response(JSON.stringify({ error: "This is the last active local administrator; make another one first", code: "last_admin" }), { status: 409 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Disable ann" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/last active local administrator/);
  });

  it("offers reattach only on synced people who need it", async () => {
    mockFetch({
      "GET /api/admin/users": () =>
        users(
          person({ id: "u2", username: "sam", source: "kysignon", status: "inactive", bound_to: "kyidentity https://a.example", needs_reattach: true }),
          person({ id: "u3", username: "tia", source: "scim", bound_to: "kyidentity https://b.example", needs_reattach: false }),
          person({ id: "u4", username: "lou", needs_reattach: false }),
        ),
    });
    render(<People me="u0" />);
    expect(await screen.findByRole("button", { name: "Reattach sam to current sign-in" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Reattach tia/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Reattach lou/ })).toBeNull();
  });

  it("names both bindings before reattaching, and reattaches only when confirmed", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    let posts = 0;
    let sent: unknown;
    mockFetch({
      "GET /api/admin/users": () => users(person({ id: "u2", username: "sam", source: "kysignon", status: "inactive", bound_to: "kyidentity https://a.example", needs_reattach: true })),
      "POST /api/admin/users/u2/reattach": (init) => {
        posts++;
        sent = JSON.parse(String(init?.body));
        return person({ id: "u2", username: "sam", source: "kysignon", bound_to: "kyidentity https://b.example" });
      },
    });
    render(<People me="u0" />);
    const button = await screen.findByRole("button", { name: "Reattach sam to current sign-in" });
    fireEvent.click(button);
    const text = String(confirm.mock.calls[0][0]);
    expect(text).toContain("kyidentity https://a.example");
    expect(text).toContain("kyidentity https://b.example");
    expect(text).toMatch(/same person/);
    expect(text).toMatch(/signs in with this account's subject gets this account and its calendars/);
    expect(posts).toBe(0);
    fireEvent.click(button);
    await waitFor(() => expect(posts).toBe(1));
    expect(sent).toEqual({ binding: "kyidentity https://b.example" });
  });

  it("reloads with an alert when the sign-in changed since the confirm", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    let loads = 0;
    mockFetch({
      "GET /api/admin/users": () => {
        loads++;
        return users(person({ id: "u2", username: "sam", source: "kysignon", bound_to: "kyidentity https://a.example", needs_reattach: true }));
      },
      "POST /api/admin/users/u2/reattach": () =>
        new Response(JSON.stringify({ error: "The sign-in provider changed since you confirmed; review the person again", code: "binding_changed" }), { status: 409 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reattach sam to current sign-in" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign-in provider changed/);
    await waitFor(() => expect(loads).toBe(2));
  });

  it("asks for a fresh sign-in when a reattach needs step-up", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/users": () => users(person({ id: "u2", username: "sam", source: "scim", bound_to: "", needs_reattach: true })),
      "POST /api/admin/users/u2/reattach": () => new Response(JSON.stringify({ error: "Sign in again to reattach a person", code: "reauth_required" }), { status: 403 }),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Reattach sam to current sign-in" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  // Small pages: the client pages by the rows it holds, whatever the server's page size.
  const many = (prefix: string, from: number, n: number) =>
    Array.from({ length: n }, (_, i) => person({ id: `${prefix}${from + i}`, username: `${prefix}${from + i}` }));
  const page = (list: unknown[], total: number) => ({ users: list, total, signin_binding: "" });

  it("pages with Load more, and a search starts over", async () => {
    mockFetch({
      "GET /api/admin/users": () => page(many("p", 0, 3), 5),
      "GET /api/admin/users?offset=3": () => page(many("p", 3, 2), 5),
      "GET /api/admin/users?q=s": () => page(many("s", 0, 3), 4),
      "GET /api/admin/users?q=s&offset=3": () => page(many("s", 3, 1), 4),
    });
    render(<People me="u0" />);
    expect(await screen.findByText("Showing 3 of 5")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("p4")).toBeTruthy();
    expect(screen.queryByText(/^Showing/)).toBeNull();
    expect(screen.getByText("p0")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();

    fireEvent.change(screen.getByLabelText("Search people"), { target: { value: "s" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("Showing 3 of 4")).toBeTruthy();
    expect(screen.queryByText("p0")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("s3")).toBeTruthy();
    expect(screen.queryByText(/^Showing/)).toBeNull();
  });

  it("appends a page once on a double click", async () => {
    let calls = 0;
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    mockFetch({
      "GET /api/admin/users": () => page(many("p", 0, 3), 7),
      "GET /api/admin/users?offset=3": async () => {
        calls++;
        await gate;
        return page(many("p", 3, 2), 7);
      },
    });
    render(<People me="u0" />);
    const more = await screen.findByRole("button", { name: "Load more" });
    fireEvent.click(more);
    fireEvent.click(more);
    await waitFor(() => expect((more as HTMLButtonElement).disabled).toBe(true));
    release();
    expect(await screen.findByText("Showing 5 of 7")).toBeTruthy();
    expect(calls).toBe(1);
    expect(screen.getAllByText("p3")).toHaveLength(1);
  });

  it("drops a Load more that a search overtook", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    mockFetch({
      "GET /api/admin/users": () => page(many("p", 0, 3), 5),
      "GET /api/admin/users?offset=3": async () => {
        await gate;
        return page(many("p", 3, 2), 5);
      },
      "GET /api/admin/users?q=zed": () => page([person({ id: "z", username: "zed" })], 1),
    });
    render(<People me="u0" />);
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    fireEvent.change(screen.getByLabelText("Search people"), { target: { value: "zed" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(await screen.findByText("zed")).toBeTruthy();
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByText("p3")).toBeNull();
    expect(screen.queryByText("p0")).toBeNull();
    expect(screen.queryByText(/^Showing/)).toBeNull();
  });
});
