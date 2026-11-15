/**
 * sessionStorage key carrying a sign-in error across federated logout.
 *
 * The Keycloak post_logout_redirect_uri is fixed to the bare /signin (it must
 * match a registered URI exactly, so no ?error= query survives the round
 * trip). When the refresh-failure path signs out an expired session, the
 * client stashes "session_expired" here first (see lib/federated-logout.ts)
 * and /signin consumes and clears it, so the "session expired" hint survives.
 * Tab-scoped storage: nothing lingers for the next user of a shared machine
 * once the tab closes, and the value is still mapped through
 * signInErrorMessage (never echoed raw).
 */
export const PENDING_SIGNIN_ERROR_KEY = "fc-signin-error";

/**
 * Maps the `error` query param on /signin to user-facing copy.
 *
 * Producers: Auth.js failures land on /signin?error=auth_failed (src/auth.ts
 * `pages.error`) and expired sessions on /signin?error=session_expired
 * (src/app/providers.tsx). Kept dependency-free so it can be unit-tested in
 * a plain node environment (see src/lib/csp.ts for the same convention).
 *
 * The raw param is never echoed back: unknown values get a generic message,
 * so a crafted link cannot put arbitrary text on the sign-in page.
 */
export function signInErrorMessage(error: string | null | undefined): string | null {
  if (!error) return null;
  if (error === "session_expired") {
    return "Your session has expired. Sign in again to continue.";
  }
  if (error === "auth_failed") {
    return "Sign-in failed. Try again — if this keeps happening, the identity provider may be unreachable; contact your administrator.";
  }
  return "Sign-in failed. Please try again.";
}
