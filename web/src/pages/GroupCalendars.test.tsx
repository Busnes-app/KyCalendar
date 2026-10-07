import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import GroupCalendars from "./GroupCalendars";

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
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

const team = { id: "cal_1", name: "Team", color: "", description: "", created_at: "2026-10-06T00:00:00Z", grants: [] };
const groups = { groups: [{ id: "grp_1", display_name: "Sales" }], total: 1 };

describe("GroupCalendars", () => {
  it("grants a group a role", async () => {
    const put = vi.fn((init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toEqual({ role: "editor" });
      return [{ group_id: "grp_1", group_name: "Sales", role: "editor" }];
    });
    mockFetch({
      "GET /api/admin/calendars": () => [team],
      "GET /api/admin/groups": () => groups,
      "PUT /api/calendars/cal_1/grants/grp_1": put,
    });
    render(<GroupCalendars />);
    await screen.findByRole("heading", { name: "Team" });
    fireEvent.change(screen.getByLabelText("Group"), { target: { value: "grp_1" } });
    fireEvent.change(screen.getByLabelText("Role"), { target: { value: "editor" } });
    fireEvent.click(screen.getByRole("button", { name: /add group/i }));
    await waitFor(() => expect(put).toHaveBeenCalled());
  });

  it("asks for a fresh sign-in when delete needs step-up", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mockFetch({
      "GET /api/admin/calendars": () => [team],
      "GET /api/admin/groups": () => groups,
      "DELETE /api/admin/calendars/cal_1": () =>
        new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<GroupCalendars />);
    fireEvent.click(await screen.findByRole("button", { name: /delete calendar/i }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("alerts when the list cannot load", async () => {
    mockFetch({
      "GET /api/admin/calendars": () => new Response("{}", { status: 500 }),
      "GET /api/admin/groups": () => groups,
    });
    render(<GroupCalendars />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });
});
