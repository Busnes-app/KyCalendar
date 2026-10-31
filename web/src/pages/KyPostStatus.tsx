import { useEffect, useState } from "react";
import { send } from "../admin";

interface Status {
  connected: boolean;
  source?: string;
  url?: string;
  reason?: string;
  scheduling: boolean;
  scheduling_mode: string;
}

function isStatus(v: unknown): v is Status {
  if (typeof v !== "object" || v === null) return false;
  const opt = (k: string) => !(k in v) || typeof (v as Record<string, unknown>)[k] === "string";
  return "connected" in v && typeof v.connected === "boolean" && "scheduling" in v && typeof v.scheduling === "boolean" &&
    "scheduling_mode" in v && typeof v.scheduling_mode === "string" && opt("source") && opt("url") && opt("reason");
}

const REASONS: Record<string, string> = {
  pending: "Not checked yet.",
  scheduling_off: "Scheduling is off (KYCALENDAR_SCHEDULING=false), so KyPost is not looked up.",
  no_kyidentity: "Sign-in is not Single Sign On (KyIdentity). Use it, or set KYCALENDAR_KYPOST_URL and KYCALENDAR_KYPOST_SECRET_FILE.",
  no_peer: "KyIdentity has no KyPost paired with KyCalendar.",
  unauthorized: "KyIdentity refused KyCalendar's client ID and secret.",
  unreachable: "KyIdentity could not be reached. Retrying.",
  invalid_peer: "KyIdentity's KyPost address is not a plain https:// address.",
  no_credentials: "The sign-in client secret could not be read.",
};

function connection(s: Status): string {
  if (!s.connected) return `Not connected: ${REASONS[s.reason ?? ""] ?? s.reason ?? "unknown reason"}`;
  return s.source === "environment" ? "Connected through KYCALENDAR_KYPOST_URL" : "Connected through KyIdentity";
}

function scheduling(s: Status): string {
  const how = s.scheduling_mode === "auto" ? "automatic, decided at startup" : "set by KYCALENDAR_SCHEDULING";
  return `${s.scheduling ? "On" : "Off"} (${how})`;
}

function hint(s: Status): string | null {
  if (s.scheduling_mode === "auto" && !s.scheduling && s.connected) return "Restart KyCalendar to turn scheduling on.";
  if (s.scheduling && !s.connected) return "Invitations to people outside this server fail until KyPost is connected.";
  return null;
}

/** Read-only: how KyCalendar reaches KyPost for invitations. The shared secret never reaches the browser. */
export default function KyPostStatus() {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    void (async () => {
      const res = await send("/api/admin/kypost");
      const body: unknown = res?.ok ? await res.json().catch(() => null) : null;
      if (isStatus(body)) setStatus(body);
      else setError(true);
    })();
  }, []);

  return (
    <section className="card" aria-labelledby="kypost-title">
      <div className="card-header">
        <h2 className="card-title" id="kypost-title">KyPost</h2>
        {status && <span className="ky-status" data-ky-status={status.connected ? "success" : undefined}>{status.connected ? "Connected" : "Not connected"}</span>}
      </div>
      <div className="card-body">
        {error && <p role="alert">Could not load the KyPost status. Reload the page to try again.</p>}
        {status && (
          <>
            <dl className="facts">
              <dt>Connection</dt>
              <dd>{connection(status)}</dd>
              {status.url && (
                <>
                  <dt>Address</dt>
                  <dd className="font-mono">{status.url}</dd>
                </>
              )}
              <dt>Scheduling</dt>
              <dd>{scheduling(status)}</dd>
            </dl>
            {hint(status) && <p role="note">{hint(status)}</p>}
          </>
        )}
      </div>
    </section>
  );
}
