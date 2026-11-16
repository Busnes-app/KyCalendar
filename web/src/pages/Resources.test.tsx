import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Resources from "./Resources";

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

const fjord = {
  id: "a".repeat(32), address: `resource-${"a".repeat(32)}@kycalendar.invalid`, kind: "room", name: "Fjord", location: "Floor 2",
  capacity: 8, tags: ["screen"], audience: "groups", groups: ["grp_1"], approval: "approvers", approvers: ["usr_1"],
  max_minutes: 120, horizon_days: 60, enabled: false, group_refs: [{ id: "grp_1", display_name: "Sales" }],
  approver_refs: [{ id: "usr_1", username: "ann", display_name: "Ann" }],
};
const groups = { groups: [{ id: "grp_1", display_name: "Sales" }], total: 1 };

describe("Resources", () => {
  it("shows who may book, the policy, the limits and the address", async () => {
    mockFetch({ "GET /api/admin/resources": () => ({ resources: [fjord] }), "GET /api/admin/groups?limit=200": () => groups });
    render(<Resources />);
    await screen.findByRole("heading", { name: /Fjord/ });
    expect(screen.getByText("Disabled")).toBeTruthy();
    expect(screen.getByText("Sales")).toBeTruthy();
    expect(screen.getByText("Approved by Ann")).toBeTruthy();
    expect(screen.getByText("Up to 2 h, at most 60 days ahead")).toBeTruthy();
    expect(screen.getByText(fjord.address)).toBeTruthy();
  });

  it("creates a room with tags split on commas", async () => {
    let sent: Record<string, unknown> = {};
    const list = vi.fn(() => ({ resources: [] }));
    mockFetch({
      "GET /api/admin/resources": list,
      "GET /api/admin/groups?limit=200": () => groups,
      "POST /api/admin/resources": (init) => {
        sent = JSON.parse(String(init?.body));
        return new Response(JSON.stringify(fjord), { status: 201 });
      },
    });
    render(<Resources />);
    fireEvent.click(await screen.findByRole("button", { name: "New room or equipment" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Fjord" } });
    fireEvent.change(screen.getByLabelText("Equipment tags"), { target: { value: "screen, , video" } });
    fireEvent.click(screen.getByLabelText(/Only these groups/));
    fireEvent.click(screen.getByLabelText(/Sales/));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    expect(sent).toMatchObject({ kind: "room", name: "Fjord", tags: ["screen", "video"], audience: "groups", groups: ["grp_1"], approval: "auto", enabled: true });
  });

  it("shows the server's refusal and keeps the editor open", async () => {
    mockFetch({
      "GET /api/admin/resources": () => ({ resources: [] }),
      "GET /api/admin/groups?limit=200": () => groups,
      "POST /api/admin/resources": () => new Response(JSON.stringify({ error: "A room or equipment with this name already exists", code: "name_taken" }), { status: 409 }),
    });
    render(<Resources />);
    fireEvent.click(await screen.findByRole("button", { name: "New room or equipment" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Fjord" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toBe("A room or equipment with this name already exists");
    expect(screen.getByLabelText("Name")).toBeTruthy();
  });
});
