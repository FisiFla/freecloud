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

/**
 * Split-horizon Keycloak access (AUTH_KEYCLOAK_INTERNAL_URL).
 *
 * The issuer is a browser-facing URL (KC_HOSTNAME): browsers must reach
 * it for the authorize redirect, and tokens carry it as `iss`. On the
 * localhost dev stack that URL (http://localhost:8081) is NOT reachable
 * from inside this container — localhost is the container itself — so
 * every server-side OIDC call (discovery, token exchange, JWKS,
 * refresh) failed with "fetch failed" and sign-in never completed.
 *
 * When AUTH_KEYCLOAK_INTERNAL_URL is set (e.g. http://keycloak:8080),
 * server-side fetches whose origin exactly matches the issuer origin
 * are rewritten to the internal origin. The discovery document still
 * declares the public issuer, so iss validation is unaffected, and the
 * authorization endpoint (a browser redirect, never fetched) stays
 * public. Unset (prod, single public URL) → no rewriting anywhere.
 */
export function createKeycloakFetch(
  issuer: string,
  internalBase: string | undefined,
  baseFetch: typeof fetch,
): typeof fetch | undefined {
  if (!internalBase) {
    return undefined;
  }
  let issuerOrigin: string;
  let internalOrigin: string;
  try {
    issuerOrigin = new URL(issuer).origin;
    internalOrigin = new URL(internalBase).origin;
  } catch {
    return undefined;
  }
  if (issuerOrigin === "null" || internalOrigin === "null") {
    return undefined;
  }
  const rewrite = (raw: string): string => {
    const u = new URL(raw);
    if (u.origin !== issuerOrigin) {
      return raw;
    }
    return internalOrigin + u.pathname + u.search + u.hash;
  };
  return (input, init) => {
    if (typeof input === "string") {
      return baseFetch(rewrite(input), init);
    }
    if (input instanceof URL) {
      return baseFetch(new URL(rewrite(input.toString())), init);
    }
    return baseFetch(new Request(rewrite(input.url), input), init);
  };
}

// Resolve the global fetch at CALL time, never capture it at module
// load: the runtime (and tests) may replace globalThis.fetch after
// import, and the pre-existing refresh path always used the live one.
const liveFetch: typeof fetch = (input, init) => fetch(input, init);

/**
 * The fetch Auth.js should use for Keycloak server-side calls, or
 * undefined to keep the default global fetch (no internal URL set).
 */
export const keycloakCustomFetch: typeof fetch | undefined = createKeycloakFetch(
  keycloakIssuer,
  process.env.AUTH_KEYCLOAK_INTERNAL_URL,
  liveFetch,
);

// Server-side fetch honouring the split-horizon rewrite (token refresh).
const keycloakServerFetch: typeof fetch = keycloakCustomFetch ?? liveFetch;

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
    const res = await keycloakServerFetch(`${keycloakIssuer}/protocol/openid-connect/token`, {
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
