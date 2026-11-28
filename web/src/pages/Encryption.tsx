import { useCallback, useEffect, useState } from "react";
import { secureFetch } from "../api";
import { REAUTH } from "../admin";
import { CopyButton } from "../components/ui";

export interface EncryptionStatus {
  enabled: boolean;
  set_up?: boolean;
  unlocked?: boolean;
  transition_until?: string | null;
}

/** encryptionStatus is the server's content encryption state for this session, or null. */
export async function encryptionStatus(): Promise<EncryptionStatus | null> {
  const res = await secureFetch("/api/encryption").catch(() => null);
  return res?.ok ? res.json() : null;
}

const post = (url: string, body?: unknown) =>
  secureFetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  }).catch(() => null);

async function errorOf(res: Response | null, fallback: string) {
  const body = await res?.json().catch(() => null);
  return body?.code === "reauth_required" ? REAUTH : (body?.error ?? fallback);
}

/** Encryption: set up, unlock, and the 14-day transition for existing app passwords. */
export default function Encryption() {
  const [status, setStatus] = useState<EncryptionStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const load = useCallback(async () => {
    setFailed(false);
    const s = await encryptionStatus();
    setStatus(s);
    setFailed(s === null);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  if (failed)
    return (
      <>
        <p role="alert" className="error">
          Could not load the encryption status.
        </p>
        <button type="button" onClick={() => void load()}>
          Try again
        </button>
      </>
    );
  if (!status) return <p className="field-hint">Loading…</p>;
  if (!status.enabled) return <p className="field-hint">Encryption is not turned on for this server.</p>;
  return (
    <>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {!status.set_up && <Setup onDone={load} onError={setError} />}
      {status.set_up && !status.unlocked && <Unlock onDone={load} onError={setError} />}
      {status.set_up && status.unlocked && <Unlocked status={status} onDone={load} onError={setError} />}
    </>
  );
}

type Step = { onDone: () => void; onError: (e: string | null) => void };

function Setup({ onDone, onError }: Step) {
  const [code, setCode] = useState<string | null>(null);
  const [typed, setTyped] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [again, setAgain] = useState("");

  async function begin() {
    onError(null);
    const res = await post("/api/encryption/recovery-code");
    if (!res?.ok) return onError(await errorOf(res, "Could not start setup"));
    setCode((await res.json()).recovery_code);
  }

  const norm = (s: string) => s.toUpperCase().replace(/[\s-]/g, "");
  const ready = code !== null && norm(typed) === norm(code) && passphrase.length >= 12 && passphrase === again;

  async function finish(e: React.FormEvent) {
    e.preventDefault();
    onError(null);
    const res = await post("/api/encryption/setup", { passphrase, recovery_code: code });
    if (!res?.ok) return onError(await errorOf(res, "Could not set up encryption"));
    onDone();
  }

  return (
    <div className="card">
      <h2 className="section-title">Encrypt your calendars</h2>
      <p className="section-lede">
        Titles, descriptions, locations and notes are stored encrypted, so a copy of the database or a backup does not show them.
        Times, repeats and attendees stay readable to the server, so free/busy, booking pages, rooms and invitations keep working.
      </p>
      <p className="field-hint">
        There is no reset by an administrator. Lose your passphrase and your recovery code and the encrypted content is gone; the times stay.
      </p>
      {code === null ? (
        <div className="form-actions">
          <button type="button" onClick={() => void begin()}>
            Set up encryption
          </button>
        </div>
      ) : (
        <form onSubmit={finish} className="form form-wide">
          <p>Write down your recovery code. It is shown only now.</p>
          <div className="secret">
            <code>{code}</code>
            <CopyButton text={code} label="Copy the recovery code" />
          </div>
          <label>
            Type the recovery code again
            <input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
          </label>
          <label>
            Passphrase (at least 12 characters)
            <input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} autoComplete="new-password" />
          </label>
          <label>
            Passphrase again
            <input type="password" value={again} onChange={(e) => setAgain(e.target.value)} autoComplete="new-password" />
          </label>
          <p className="field-hint">A long passphrase matters: someone with a stolen database can try guesses offline.</p>
          <div className="form-actions">
            <button type="submit" disabled={!ready}>
              Encrypt my calendars
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

function Unlock({ onDone, onError }: Step) {
  const [useCode, setUseCode] = useState(false);
  const [secret, setSecret] = useState("");

  async function unlock(e: React.FormEvent) {
    e.preventDefault();
    onError(null);
    const res = await post("/api/encryption/unlock", useCode ? { recovery_code: secret } : { passphrase: secret });
    if (!res?.ok) return onError(await errorOf(res, "Could not unlock"));
    setSecret("");
    onDone();
  }

  return (
    <div className="card">
      <h2 className="section-title">Unlock your calendars</h2>
      <p className="section-lede">This browser has not unlocked your encrypted calendars yet.</p>
      <form onSubmit={unlock} className="form">
        <label>
          {useCode ? "Recovery code" : "Passphrase"}
          <input
            type={useCode ? "text" : "password"}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            autoComplete={useCode ? "off" : "current-password"}
          />
        </label>
        <div className="form-actions">
          <button type="submit" disabled={!secret}>
            Unlock
          </button>
          <button type="button" className="button-secondary" onClick={() => setUseCode(!useCode)}>
            {useCode ? "Use passphrase" : "Use recovery code"}
          </button>
        </div>
      </form>
    </div>
  );
}

function Unlocked({ status, onDone, onError }: Step & { status: EncryptionStatus }) {
  const until = status.transition_until ? new Date(status.transition_until) : null;
  const open = until !== null && until > new Date();

  async function endNow() {
    if (!window.confirm("App passwords that have not synced since you set up encryption will stop working. End the transition now?")) return;
    onError(null);
    const res = await post("/api/encryption/end-transition");
    if (!res?.ok) return onError(await errorOf(res, "Could not end the transition"));
    onDone();
  }

  return (
    <div className="card">
      <h2 className="section-title">Your calendars are encrypted</h2>
      <p className="section-lede">This browser is unlocked.</p>
      {until && open && (
        <>
          <p className="field-hint">
            Until {until.toLocaleDateString()}, app passwords made before encryption keep working, each once it syncs. Any that has not synced by then stops,
            and you create a new one in Phones &amp; apps.
          </p>
          <div className="form-actions">
            <button type="button" className="button-secondary" onClick={() => void endNow()}>
              End the transition now
            </button>
          </div>
        </>
      )}
    </div>
  );
}
