import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Encryption from "./Encryption";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockFetch(handlers: Record<string, (init?: RequestInit) => unknown>) {
  const calls: string[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    calls.push(`${key} ${init?.body ?? ""}`);
    const handler = handlers[key];
    if (!handler) throw new Error(`unexpected ${key}`);
    const body = handler(init);
    return new Response(body === null ? null : JSON.stringify(body), { status: body === null ? 204 : 200 });
  });
  return calls;
}

describe("Encryption", () => {
  it("sets up only after the recovery code is typed back", async () => {
    let status = { enabled: true, set_up: false, unlocked: false };
    const calls = mockFetch({
      "GET /api/encryption": () => status,
      "POST /api/encryption/recovery-code": () => ({ recovery_code: "ABCD-EFGH-IJKL-MNOP-QRSTU-VWXYZ" }),
      "POST /api/encryption/setup": () => {
        status = { enabled: true, set_up: true, unlocked: true };
        return null;
      },
    });
    render(<Encryption />);
    fireEvent.click(await screen.findByRole("button", { name: /set up encryption/i }));
    expect(await screen.findByText("ABCD-EFGH-IJKL-MNOP-QRSTU-VWXYZ")).toBeTruthy();
    const submit = screen.getByRole("button", { name: /encrypt my calendars/i }) as HTMLButtonElement;
    fireEvent.change(screen.getByLabelText(/^passphrase \(/i), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText(/passphrase again/i), { target: { value: "correct horse battery" } });
    expect(submit.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText(/type the recovery code/i), { target: { value: "abcdefghijklmnopqrstuvwxyz" } });
    expect(submit.disabled).toBe(false);
    fireEvent.click(submit);
    expect(await screen.findByText(/your calendars are encrypted/i)).toBeTruthy();
    expect(calls.some((c) => c.startsWith("POST /api/encryption/setup") && c.includes("correct horse battery"))).toBe(true);
  });

  it("unlocks with the recovery code", async () => {
    let status = { enabled: true, set_up: true, unlocked: false };
    const calls = mockFetch({
      "GET /api/encryption": () => status,
      "POST /api/encryption/unlock": () => {
        status = { enabled: true, set_up: true, unlocked: true };
        return null;
      },
    });
    render(<Encryption />);
    fireEvent.click(await screen.findByRole("button", { name: /use recovery code/i }));
    fireEvent.change(screen.getByLabelText(/recovery code/i), { target: { value: "ABCD" } });
    fireEvent.click(screen.getByRole("button", { name: /^unlock$/i }));
    await waitFor(() => expect(screen.getByText(/your calendars are encrypted/i)).toBeTruthy());
    expect(calls.some((c) => c.includes('"recovery_code":"ABCD"'))).toBe(true);
  });

  it("says when the server has it off", async () => {
    mockFetch({ "GET /api/encryption": () => ({ enabled: false }) });
    render(<Encryption />);
    expect(await screen.findByText(/not turned on/i)).toBeTruthy();
  });
});
