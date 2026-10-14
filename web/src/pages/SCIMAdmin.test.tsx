import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SCIMAdmin } from "./SCIMAdmin";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, () => unknown>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler();
    if (body instanceof Response) return body;
    return new Response(JSON.stringify(body), { status: 200 });
  });
}

describe("SCIMAdmin", () => {
  it("shows the status and accounts from the admin route, never a token", async () => {
    const fetch = mockFetch({
      "GET /api/admin/scim": () => ({
        enabled: true,
        token_configured: true,
        endpoint: "https://cal.example/scim/v2",
        users: [{ id: "u1", username: "ann", display_name: "Ann", email: "ann@example.com", status: "active" }],
        total: 1,
      }),
    });
    render(<SCIMAdmin />);
    expect(await screen.findByText("ann")).toBeTruthy();
    expect(screen.getByText("Enabled")).toBeTruthy();
    expect(screen.getByText(/bearer token is set/i)).toBeTruthy();
    expect((screen.getByLabelText("SCIM base URL") as HTMLInputElement).value).toBe("https://cal.example/scim/v2");
    expect(screen.queryByRole("button")).toBeNull();
    expect(fetch.mock.calls.map(([url]) => String(url))).toEqual(["/api/admin/scim"]);
  });

  it("says how to enable SCIM when no token is set", async () => {
    mockFetch({ "GET /api/admin/scim": () => ({ enabled: false, token_configured: false, endpoint: "/scim/v2", users: [], total: 0 }) });
    render(<SCIMAdmin />);
    expect(await screen.findByText("Disabled")).toBeTruthy();
    expect(screen.getByText(/Set KY_SCIM_TOKEN/)).toBeTruthy();
  });

  it("alerts when the status cannot load", async () => {
    mockFetch({ "GET /api/admin/scim": () => new Response("{}", { status: 500 }) });
    render(<SCIMAdmin />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/could not load/i);
  });
});
