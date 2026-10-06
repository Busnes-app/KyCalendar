import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import AppPasswords from "./AppPasswords";

afterEach(() => vi.restoreAllMocks());

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
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
});
