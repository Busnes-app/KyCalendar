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
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const person = (over: Record<string, unknown>) => ({
  id: "u1", username: "ann", display_name: "Ann", email: "", role: "user", status: "active", source: "local",
  mfa: false, must_change_password: false, last_login_at: null, ...over,
});
const users = (...list: unknown[]) => ({ users: list, total: list.length });

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
});
