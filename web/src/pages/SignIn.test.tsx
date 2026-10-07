import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import SignIn from "./SignIn";

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

const field = (value: string, source = "saved") => ({ value, source });
const saved = {
  provider: field("kyidentity"),
  display_name: field("KyIdentity"),
  issuer: field("https://id.example"),
  client_id: field("kc"),
  client_secret: { set: true, source: "saved" },
  callback_url: "https://cal.example/api/sso/kysignon/callback",
  sso_enabled: true,
  live: true,
};

describe("SignIn", () => {
  it("locks the fields the environment sets", async () => {
    mockFetch({
      "GET /api/admin/signin": () => ({
        ...saved,
        provider: field("kyidentity", "environment"),
        issuer: field("https://id.example", "environment"),
        client_id: field("kc", "environment"),
        client_secret: { set: true, source: "environment" },
      }),
    });
    render(<SignIn />);
    expect((await screen.findByLabelText<HTMLSelectElement>("Provider")).disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Issuer URL").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Client ID").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Client secret").disabled).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>("Button label").disabled).toBe(false);
    expect(screen.getByText(/KY_KYSIGNON_ISSUER/)).toBeTruthy();
    expect(screen.getByText("https://cal.example/api/sso/kysignon/callback")).toBeTruthy();
  });

  it("keeps a stored secret unless a new one is typed", async () => {
    let body: Record<string, unknown> = {};
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": (init) => {
        body = JSON.parse(String(init?.body));
        return saved;
      },
    });
    render(<SignIn />);
    expect((await screen.findByLabelText<HTMLInputElement>("Client secret")).placeholder).toMatch(/leave blank to keep/);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(body.client_secret).toBe(""));
  });

  it("states how many accounts a provider change disables before saving", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const bodies: Record<string, unknown>[] = [];
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": (init) => {
        bodies.push(JSON.parse(String(init?.body)));
        if (bodies.length === 1) {
          return new Response(JSON.stringify({ error: "Saving disables 3 accounts", code: "confirm_provider_change", count: 3 }), { status: 409 });
        }
        return { ...saved, provider: field("oidc"), display_name: field("Acme") };
      },
    });
    render(<SignIn />);
    fireEvent.change(await screen.findByLabelText("Provider"), { target: { value: "oidc" } });
    fireEvent.change(screen.getByLabelText("Button label"), { target: { value: "Acme" } });
    fireEvent.change(screen.getByLabelText("Client secret"), { target: { value: "s3cret" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("status")).textContent).toMatch(/Continue with Acme/);
    expect(confirm.mock.calls[0][0]).toMatch(/disables 3 accounts/);
    expect(bodies.map((b) => b.confirm_disable)).toEqual([0, 3]);
  });

  it("asks for a fresh sign-in when saving needs step-up", async () => {
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": () => new Response(JSON.stringify({ error: "Sign in again", code: "reauth_required" }), { status: 403 }),
    });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/sign in again/i);
  });

  it("shows why a test failed", async () => {
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "POST /api/admin/signin/test": () =>
        new Response(JSON.stringify({ error: "the issuer resolves to a loopback or link-local address, which is refused" }), { status: 422 }),
    });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Test" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/loopback or link-local/);
  });

  it("surfaces the server's refusal when a changed provider needs a new secret", async () => {
    mockFetch({
      "GET /api/admin/signin": () => saved,
      "PUT /api/admin/signin": () => new Response(JSON.stringify({ error: "Enter the client secret for this provider" }), { status: 400 }),
    });
    render(<SignIn />);
    fireEvent.change(await screen.findByLabelText("Client ID"), { target: { value: "other" } });
    fireEvent.change(screen.getByLabelText("Client secret"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/Enter the client secret/);
  });

  it("never renders a secret and requires one when none is stored", async () => {
    mockFetch({ "GET /api/admin/signin": () => ({ ...saved, client_secret: { set: false, source: "unset" } }) });
    render(<SignIn />);
    const secret = await screen.findByLabelText<HTMLInputElement>("Client secret");
    expect(secret.value).toBe("");
    expect(secret.required).toBe(true);
    expect(secret.placeholder).not.toMatch(/leave blank/);
  });

  it("copies the callback URL", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    mockFetch({ "GET /api/admin/signin": () => saved });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(saved.callback_url));
  });

  it("requires a new secret as soon as the registration changes", async () => {
    mockFetch({ "GET /api/admin/signin": () => saved });
    render(<SignIn />);
    const secret = await screen.findByLabelText<HTMLInputElement>("Client secret");
    expect(secret.required).toBe(false);
    fireEvent.change(screen.getByLabelText("Issuer URL"), { target: { value: " https://id.example " } });
    expect(secret.required).toBe(false);
    // Issuers compare exactly: a trailing slash is another issuer.
    fireEvent.change(screen.getByLabelText("Issuer URL"), { target: { value: "https://id.example/" } });
    expect(secret.required).toBe(true);
    fireEvent.change(screen.getByLabelText("Issuer URL"), { target: { value: "https://other.example" } });
    expect(secret.required).toBe(true);
    expect(secret.placeholder).toBe("Required");
  });

  it("shows an alert when the clipboard is unavailable", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    mockFetch({ "GET /api/admin/signin": () => saved });
    render(<SignIn />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/Could not copy/);
  });
});
