/**
 * Client-side federated sign-out.
 *
 * POSTs to /api/auth/federated-logout, which clears the local Auth.js session
 * and answers with the Keycloak end_session URL; the browser then navigates
 * there top-level so the SSO session ends too. When anything goes wrong the
 * helper falls back to Auth.js's local-only signOut so the dashboard session
 * is never left behind.
 */
import { PENDING_SIGNIN_ERROR_KEY } from "./signin-error";

export interface FederatedSignOutOptions {
  /**
   * The session expired (refresh failed). The sign-in page shows the
   * "session expired" hint: via sessionStorage on the federated path (the
   * post_logout_redirect_uri is fixed to the bare /signin, so no query
   * survives the Keycloak round trip) and via ?error= on the local fallback.
   */
  expired?: boolean;
}

const FEDERATED_LOGOUT_PATH = "/api/auth/federated-logout";

/** Only http(s) navigation targets from the logout route are honored. */
function isNavigableTarget(url: string): boolean {
  try {
    const protocol = new URL(url, window.location.origin).protocol;
    return protocol === "http:" || protocol === "https:";
  } catch {
    return false;
  }
}

export async function federatedSignOut(options?: FederatedSignOutOptions): Promise<void> {
  if (options?.expired) {
    try {
      window.sessionStorage.setItem(PENDING_SIGNIN_ERROR_KEY, "session_expired");
    } catch {
      // Storage unavailable (private mode, cookies blocked) — the sign-in
      // page simply won't show the expired hint.
    }
  }
  try {
    const res = await fetch(FEDERATED_LOGOUT_PATH, { method: "POST" });
    if (res.ok) {
      const data = (await res.json()) as { logoutUrl?: unknown };
      if (typeof data.logoutUrl === "string" && data.logoutUrl && isNavigableTarget(data.logoutUrl)) {
        window.location.assign(data.logoutUrl);
        return;
      }
    }
  } catch {
    // Network failure — fall through to local-only sign-out below.
  }
  const { signOut } = await import("next-auth/react");
  await signOut({ callbackUrl: options?.expired ? "/signin?error=session_expired" : "/signin" });
}
