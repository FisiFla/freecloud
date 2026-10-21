/**
 * Auth.js session-token logic: the JWT callback and the session lifetime.
 *
 * It lives in its own module (rather than in src/auth.ts) so it can be
 * unit-tested without pulling in next-auth's server runtime, and so both the
 * Auth.js configuration and the BFF proxy route can share the refresh
 * helper.
 */
import type { JWT } from "@auth/core/jwt";
import { refreshAccessToken, type RefreshableToken } from "./keycloak";

/**
 * Session lifetime in seconds (8 hours).
 *
 * Auth.js defaults to 30 days with no idle timeout (see
 * @auth/core/lib/init.js), which is far too long for an admin dashboard
 * holding a live Keycloak access token. The value bounds both the session
 * JWT's `exp` and the session cookie's lifetime.
 */
export const SESSION_MAX_AGE_SECONDS = 8 * 60 * 60;

/** The OIDC tokens Auth.js stores on the session JWT. */
type TokenWithAccess = RefreshableToken;

/** The subset of the OIDC account Auth.js hands to the jwt callback. */
type AccountTokens = {
  access_token?: string;
  id_token?: string;
  refresh_token?: string;
  expires_in?: number;
};

/**
 * The Auth.js `jwt` callback. Exported so it can be unit-tested directly —
 * the refresh-error flag handling in particular is easy to break and hard to
 * spot in the UI.
 */
export async function jwtCallback({
  token,
  account,
}: {
  token: JWT;
  account?: AccountTokens | null;
}): Promise<JWT> {
  const t = token as JWT & TokenWithAccess;

  // Initial sign-in: persist the OIDC tokens on the JWT.
  if (account) {
    t.accessToken = account.access_token;
    t.idToken = account.id_token;
    t.refreshToken = account.refresh_token;
    t.expiresAt = Math.floor(Date.now() / 1000) + (account.expires_in ?? 0);
    return t;
  }

  // Token still valid for more than 30s — keep it. The stale refresh-error
  // flag is cleared here: the previous refresh did in fact recover, and
  // leaving the flag set would permanently brick the session (the client
  // signs out on every render and the BFF answers 401 for every request).
  if (t.expiresAt && Date.now() / 1000 < t.expiresAt - 30) {
    delete t.error;
    return t;
  }

  // About to expire — refresh via Keycloak's token endpoint.
  const refreshed = await refreshAccessToken(t);
  if (refreshed.error) {
    t.error = "RefreshAccessTokenError";
    return t;
  }
  t.accessToken = refreshed.accessToken;
  t.idToken = refreshed.idToken;
  t.refreshToken = refreshed.refreshToken;
  t.expiresAt = refreshed.expiresAt;
  // A successful refresh clears any previously recorded failure.
  delete t.error;
  return t;
}
