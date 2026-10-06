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
        list = [{ id: "p1", label: "iPhone", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
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
    let list: unknown[] = [{ id: "p1", label: "iPhone", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
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
    const list = [{ id: "p1", label: "iPhone", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
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
    const list = [{ id: "p1", label: "iPhone", created_at: "2026-10-06T00:00:00Z", last_used_at: null }];
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
});
