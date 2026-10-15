import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import AppPasswords from "./AppPasswords";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    if (body instanceof Response) return body;
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : 200 });
  });
}

describe("AppPasswords", () => {
  it("shows a new password once and lists only labels", async () => {
    let list: unknown[] = [];
    mockFetch({
      "GET /api/app-passwords": () => list,
      "POST /api/app-passwords": () => {
        list = [{ id: "p1", label: "iPhone", scope: "dav", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
        return { id: "p1", label: "iPhone", password: "kc_p1_secret", created_at: "2026-10-06T00:00:00Z" };
      },
    });
    render(<AppPasswords username="alice" />);
    fireEvent.change(screen.getByLabelText(/device name/i), { target: { value: "iPhone" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(await screen.findByText("kc_p1_secret")).toBeTruthy();
    expect(screen.getByText("alice")).toBeTruthy();
    await waitFor(() => expect(screen.getAllByText("iPhone").length).toBeGreaterThan(0));
  });

  it("revokes a password", async () => {
    let list: unknown[] = [{ id: "p1", label: "iPhone", scope: "dav", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
    const del = vi.fn(() => {
      list = [];
      return null;
    });
    mockFetch({ "GET /api/app-passwords": () => list, "DELETE /api/app-passwords/p1": del });
    render(<AppPasswords username="alice" />);
    fireEvent.click(await screen.findByRole("button", { name: /revoke iphone/i }));
    await waitFor(() => expect(del).toHaveBeenCalled());
  });

  it("says so when the list cannot load", async () => {
    mockFetch({ "GET /api/app-passwords": () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }) });
    render(<AppPasswords username="alice" />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });

  it("says so when the list request fails on the network", async () => {
    mockFetch({
      "GET /api/app-passwords": () => {
        throw new TypeError("Failed to fetch");
      },
    });
    render(<AppPasswords username="alice" />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });

  it("says so when revoking fails and keeps the device listed", async () => {
    const list = [{ id: "p1", label: "iPhone", scope: "dav", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
    mockFetch({
      "GET /api/app-passwords": () => list,
      "DELETE /api/app-passwords/p1": () => new Response(JSON.stringify({ error: "nope" }), { status: 500 }),
    });
    render(<AppPasswords username="alice" />);
    fireEvent.click(await screen.findByRole("button", { name: /revoke iphone/i }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not revoke/i);
    expect(screen.getByRole("button", { name: /revoke iphone/i })).toBeTruthy();
  });

  it("says so when revoking fails on the network", async () => {
    const list = [{ id: "p1", label: "iPhone", scope: "dav", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
    mockFetch({
      "GET /api/app-passwords": () => list,
      "DELETE /api/app-passwords/p1": () => {
        throw new TypeError("Failed to fetch");
      },
    });
    render(<AppPasswords username="alice" />);
    fireEvent.click(await screen.findByRole("button", { name: /revoke iphone/i }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not revoke/i);
  });

  it("creates a calendar app password with the dav scope", async () => {
    const post = vi.fn((init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toEqual({ label: "iPhone", scope: "dav" });
      return { id: "p1", password: "kc_p1_secret" };
    });
    mockFetch({ "GET /api/app-passwords": () => [], "POST /api/app-passwords": post });
    render(<AppPasswords username="alice" />);
    fireEvent.change(screen.getByLabelText(/device name/i), { target: { value: "iPhone" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(await screen.findByText("kc_p1_secret")).toBeTruthy();
    expect(post).toHaveBeenCalled();
    expect(screen.queryByText(/openclaw\.json/)).toBeNull();
  });

  it("creates a read-only agent token by default and shows its setup once", async () => {
    let sent: unknown;
    mockFetch({
      "GET /api/app-passwords": () => [],
      "POST /api/app-passwords": (init) => {
        sent = JSON.parse(String(init?.body));
        return { id: "a1", password: "kc_a1_secret" };
      },
    });
    render(<AppPasswords username="alice" />);
    fireEvent.click(screen.getByLabelText(/ai agent/i));
    fireEvent.change(screen.getByLabelText(/agent name/i), { target: { value: "Hermes" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(await screen.findByText("kc_a1_secret")).toBeTruthy();
    expect(sent).toEqual({ label: "Hermes", scope: "mcp:read" });
    const status = screen.getByRole("status").textContent ?? "";
    expect(status).toContain(`${window.location.origin}/mcp`);
    expect(status).toContain("Bearer ${KYCAL_TOKEN}");
    expect(status).toContain(`hermes skills install ${window.location.origin}/agent/skills/kycalendar/SKILL.md`);
    expect(status).not.toMatch(/Bearer kc_/); // the token never goes into a config snippet
  });

  it("asks before letting an agent change events it did not create", async () => {
    let sent: { scope?: string } = {};
    mockFetch({
      "GET /api/app-passwords": () => [],
      "POST /api/app-passwords": (init) => {
        sent = JSON.parse(String(init?.body));
        return { id: "a1", password: "kc_a1_secret" };
      },
    });
    render(<AppPasswords username="alice" />);
    fireEvent.click(screen.getByLabelText(/ai agent/i));
    fireEvent.click(screen.getByLabelText(/read and write/i));
    const any = screen.getByLabelText(/can also change events it did not create/i) as HTMLInputElement;
    expect(any.checked).toBe(false);
    fireEvent.change(screen.getByLabelText(/agent name/i), { target: { value: "Claw" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    await screen.findByText("kc_a1_secret");
    expect(sent.scope).toBe("mcp:write-own");

    fireEvent.click(screen.getByRole("button", { name: /done/i }));
    fireEvent.click(screen.getByLabelText(/can also change events it did not create/i));
    expect(screen.getByText(/move or rewrite any of your appointments/i)).toBeTruthy();
    fireEvent.change(screen.getByLabelText(/agent name/i), { target: { value: "Claw 2" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    await screen.findByText("kc_a1_secret");
    expect(sent.scope).toBe("mcp:write-any");
  });

  it("changes an agent's access and shows when it is revoked if unused", async () => {
    let list = [
      { id: "a1", label: "Hermes", scope: "mcp:read", created_at: "2026-10-06T00:00:00Z", last_used_at: null, revoke_after: "2027-01-04T00:00:00Z" },
      { id: "p1", label: "iPhone", scope: "dav", created_at: "2026-10-06T00:00:00Z", last_used_at: null },
    ];
    const patch = vi.fn((init?: RequestInit) => {
      const { scope } = JSON.parse(String(init?.body));
      list = list.map((p) => (p.id === "a1" ? { ...p, scope } : p));
      return list[0];
    });
    mockFetch({ "GET /api/app-passwords": () => list, "PATCH /api/app-passwords/a1": patch });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<AppPasswords username="alice" />);
    const select = (await screen.findByLabelText(/access for hermes/i)) as HTMLSelectElement;
    expect(screen.queryByLabelText(/access for iphone/i)).toBeNull();
    expect(screen.getByText(/revoked if unused by/i)).toBeTruthy();
    fireEvent.change(select, { target: { value: "mcp:write-any" } });
    await waitFor(() => expect(patch).toHaveBeenCalled());
    await waitFor(() => expect((screen.getByLabelText(/access for hermes/i) as HTMLSelectElement).value).toBe("mcp:write-any"));
  });

  it("asks for a fresh sign-in when changing an agent's access needs step-up", async () => {
    const list = [{ id: "a1", label: "Hermes", scope: "mcp:read", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
    mockFetch({
      "GET /api/app-passwords": () => list,
      "PATCH /api/app-passwords/a1": () =>
        new Response(JSON.stringify({ error: "Sign in again to change an agent's access", code: "reauth_required" }), { status: 403 }),
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<AppPasswords username="alice" />);
    fireEvent.change(await screen.findByLabelText(/access for hermes/i), { target: { value: "mcp:write-own" } });
    expect(await screen.findByText(/sign in again, then try again within 10 minutes/i)).toBeTruthy();
  });

  it("asks before widening an agent's access from the list, but not before narrowing it", async () => {
    const list = [
      { id: "a1", label: "Hermes", scope: "mcp:read", created_at: "2026-10-06T00:00:00Z", last_used_at: null },
      { id: "a2", label: "Claw", scope: "mcp:write-own", created_at: "2026-10-06T00:00:00Z", last_used_at: null },
    ];
    const patch = vi.fn(() => list[0]);
    mockFetch({ "GET /api/app-passwords": () => list, "PATCH /api/app-passwords/a1": patch, "PATCH /api/app-passwords/a2": patch });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<AppPasswords username="alice" />);
    const hermes = (await screen.findByLabelText(/access for hermes/i)) as HTMLSelectElement;

    fireEvent.change(hermes, { target: { value: "mcp:write-own" } });
    expect(confirm).toHaveBeenLastCalledWith(expect.stringContaining("Anyone who can message the agent can try to make it do so."));
    expect(confirm.mock.lastCall![0]).not.toContain("move or rewrite");
    fireEvent.change(hermes, { target: { value: "mcp:write-any" } });
    expect(confirm.mock.lastCall![0]).toMatch(/Anyone who can message the agent[\s\S]*move or rewrite any of your appointments/);
    fireEvent.change(screen.getByLabelText(/access for claw/i), { target: { value: "mcp:write-any" } });
    expect(confirm.mock.lastCall![0]).toMatch(/^The agent can then move or rewrite any of your appointments\./);
    expect(confirm).toHaveBeenCalledTimes(3);
    expect(hermes.value).toBe("mcp:read");

    fireEvent.change(screen.getByLabelText(/access for claw/i), { target: { value: "mcp:read" } });
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(confirm).toHaveBeenCalledTimes(3);
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find((c) => c[1]?.method === "PATCH")!;
    expect([String(call[0]), JSON.parse(String(call[1].body))]).toEqual(["/api/app-passwords/a2", { scope: "mcp:read" }]);
  });

  it("asks for a fresh sign-in when creating needs step-up", async () => {
    mockFetch({
      "GET /api/app-passwords": () => [],
      "POST /api/app-passwords": () =>
        new Response(JSON.stringify({ error: "Sign in again to create an app password", code: "reauth_required" }), { status: 403 }),
    });
    render(<AppPasswords username="alice" />);
    fireEvent.change(screen.getByLabelText(/device name/i), { target: { value: "iPhone" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));
    expect(await screen.findByText(/sign in again, then try again within 10 minutes/i)).toBeTruthy();
  });
});
