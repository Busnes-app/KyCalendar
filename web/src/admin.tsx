import { secureFetch } from "./api";

export const JSON_HEADERS = { "Content-Type": "application/json" };

/** Resolves to null on a network error, so every caller handles one failure path. */
export const send = (url: string, init?: RequestInit) => secureFetch(url, init).catch(() => null);

export interface ApiFailure {
  error?: string;
  code?: string;
  count?: number;
}

export async function failure(res: Response | null): Promise<ApiFailure> {
  return (await res?.json().catch(() => null)) ?? {};
}

export const REAUTH = "Sign out and sign in again, then try again within 10 minutes.";

/** The badge for a synced person or group; local ones have none. */
export function managedBy(source: string): string | null {
  switch (source) {
    case "local":
      return null;
    case "scim":
      return "Managed by SCIM";
    case "kysignon":
      return "Managed by KyIdentity";
    case "oidc":
      return "Managed by the sign-in provider";
    default:
      return `Managed by ${source}`;
  }
}

export function SourceBadge({ source }: { source: string }) {
  const label = managedBy(source);
  return label ? <span className="badge">{label}</span> : null;
}
