/**
 * Keycloak OIDC client configuration and token refresh.
 *
 * Shared by the Auth.js callbacks (src/auth.ts) and the BFF proxy route
 * (src/app/api/v1/[...path]/route.ts): both need to exchange a refresh token
 * for a fresh access token, and both must do it under a timeout.
 *
 * It lives in its own module so the BFF route can refresh an expired access
 * token without importing the full Auth.js configuration (which would drag
 * the AUTH_SECRET validation into the route's module graph).
 */
import { requiredProdEnv } from "@/lib/env";

export const keycloakClientId = requiredProdEnv(
  "AUTH_KEYCLOAK_ID",
  "freecloud-dashboard",
);
// Optional: freecloud-dashboard is a PUBLIC (PKCE) client with no secret, so an
// empty value is valid (and is the turnkey default). Operators who configure a
// confidential dashboard client can set AUTH_KEYCLOAK_SECRET.
export const keycloakClientSecret = process.env.AUTH_KEYCLOAK_SECRET ?? "";
export const keycloakIssuer = requiredProdEnv(
  "AUTH_KEYCLOAK_ISSUER",
  "http://localhost:8081/realms/freecloud",
);

/** The token fields the refresh flow reads and writes. */
export interface RefreshableToken {
  accessToken?: string;
  idToken?: string;
  refreshToken?: string;
  expiresAt?: number;
  error?: "RefreshAccessTokenError";
}

/**
 * Timeout for the Keycloak token-endpoint call, in milliseconds. A hung
 * Keycloak would otherwise pin the request (and the Auth.js session read
 * that triggered it) open indefinitely. Operators can tune via
 * AUTH_REFRESH_TIMEOUT_MS.
 */
export function getRefreshTimeoutMs(): number {
  const raw = process.env.AUTH_REFRESH_TIMEOUT_MS;
  if (raw) {
    const n = Number(raw);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
  }
  return 10_000;
}

/**
 * Exchange the stored refresh token for a fresh access token.
 *
 * The caller's token is NOT mutated: on failure the returned token carries
 * `error: "RefreshAccessTokenError"` and the original fields.
 */
export async function refreshAccessToken(
  token: RefreshableToken,
): Promise<RefreshableToken> {
  const t: RefreshableToken = { ...token };
  if (!t.refreshToken) {
    t.error = "RefreshAccessTokenError";
    return t;
  }
  try {
    const res = await fetch(`${keycloakIssuer}/protocol/openid-connect/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: keycloakClientId,
        client_secret: keycloakClientSecret,
        grant_type: "refresh_token",
        refresh_token: t.refreshToken,
      }),
      signal: AbortSignal.timeout(getRefreshTimeoutMs()),
    });

    if (!res.ok) {
      t.error = "RefreshAccessTokenError";
      return t;
    }

    const tokens = (await res.json()) as {
      access_token?: string;
      id_token?: string;
      refresh_token?: string;
      expires_in?: number;
    };
    t.accessToken = tokens.access_token ?? t.accessToken;
    t.idToken = tokens.id_token ?? t.idToken;
    t.refreshToken = tokens.refresh_token ?? t.refreshToken;
    t.expiresAt = Math.floor(Date.now() / 1000) + (tokens.expires_in ?? 0);
    // The refresh succeeded — any previously recorded failure is stale.
    delete t.error;
    return t;
  } catch {
    // Network error, timeout, or malformed response — the refresh failed.
    t.error = "RefreshAccessTokenError";
    return t;
  }
}
