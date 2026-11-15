/**
 * RP-initiated (federated) logout.
 *
 * Auth.js's own signOut() clears only the dashboard session cookies — the
 * Keycloak SSO session survives, so on a shared machine the next "Sign in
 * with Keycloak" click would silently reuse the previous operator's session.
 * This route clears the local session AND answers the browser with the
 * issuer's end_session URL (id_token_hint + post_logout_redirect_uri); the
 * client navigates there top-level so the SSO session ends too.
 *
 * Static sibling of Auth.js's own /api/auth/[...nextauth] catch-all: Next.js
 * resolves the static segment first, so there is no collision.
 */
import { NextResponse, type NextRequest } from "next/server";
import { signOut } from "@/auth";
import { getToken } from "next-auth/jwt";
import { isProduction } from "@/lib/env";
import { keycloakCustomFetch, keycloakIssuer } from "@/lib/keycloak";
import { isCrossSiteRequest } from "@/lib/csrf";

export const dynamic = "force-dynamic";

const SIGNIN_PATH = "/signin";
const DISCOVERY_TIMEOUT_MS = 10_000;

/**
 * The app's own /signin on the configured public origin (AUTH_URL). NEVER
 * derived from request input (query params, Host header) — Keycloak redirects
 * the browser here after logout, so a request-controlled value would be an
 * open redirect. Returns null when AUTH_URL is missing or malformed; the
 * caller then omits the parameter and Keycloak lands on its own logged-out
 * page instead (the session still ends).
 *
 * NOTE: Keycloak only honors a post_logout_redirect_uri the dashboard client
 * allows (attribute `post.logout.redirect.uris`, falling back to the redirect
 * URIs when absent — verified against Keycloak 26 sources). The value below
 * requires the bootstrap follow-up that registers <origin>/signin; until the
 * backend backfills it, Keycloak answers the logout navigation with an error
 * page and the SSO session survives — see "Follow-ups for other lanes".
 */
function postLogoutRedirectUri(): string | null {
  const base = process.env.AUTH_URL;
  if (!base) return null;
  try {
    const url = new URL(base);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return new URL(SIGNIN_PATH, url.origin).toString();
  } catch {
    return null;
  }
}

/**
 * Resolve the issuer's end_session_endpoint from OIDC discovery (server-side,
 * honoring the split-horizon internal Keycloak URL for the fetch itself). A
 * discovered endpoint is only trusted when its origin matches the configured
 * PUBLIC issuer origin — the browser must be able to reach it. On any failure
 * this falls back to Keycloak's well-known logout path on the public issuer,
 * so federated logout degrades instead of dying: the Keycloak session must
 * still end.
 */
async function endSessionEndpoint(): Promise<string> {
  const issuer = keycloakIssuer.replace(/\/+$/, "");
  const constructed = `${issuer}/protocol/openid-connect/logout`;
  try {
    const fetchImpl = keycloakCustomFetch ?? fetch;
    const res = await fetchImpl(`${issuer}/.well-known/openid-configuration`, {
      signal: AbortSignal.timeout(DISCOVERY_TIMEOUT_MS),
      cache: "no-store",
    });
    if (!res.ok) return constructed;
    const meta = (await res.json()) as { end_session_endpoint?: unknown };
    if (typeof meta.end_session_endpoint !== "string") return constructed;
    const discovered = new URL(meta.end_session_endpoint);
    if (discovered.origin !== new URL(issuer).origin) return constructed;
    return discovered.toString();
  } catch {
    return constructed;
  }
}

export async function POST(req: NextRequest) {
  // Logout CSRF: a third-party page must not be able to force a logout.
  // Same same-origin check the BFF proxy enforces.
  if (isCrossSiteRequest(req)) {
    return NextResponse.json({ error: "Cross-site POST rejected" }, { status: 403 });
  }
  // Always clear the local Auth.js session first: whatever happens below,
  // the dashboard session must never be left behind.
  await signOut({ redirect: false });
  // Read the id token from the INCOMING request cookies: signOut() above
  // only schedules the outgoing cookie clear, so the token is still here.
  let idToken: unknown;
  try {
    const session = await getToken({
      req,
      secret: process.env.AUTH_SECRET,
      secureCookie: isProduction(),
    });
    idToken = (session as { idToken?: unknown } | null)?.idToken;
  } catch {
    // An unreadable session cookie means local logout only.
    idToken = undefined;
  }
  if (typeof idToken !== "string" || !idToken) {
    // Nothing to present to Keycloak — local logout only.
    return NextResponse.json({ logoutUrl: SIGNIN_PATH });
  }
  const target = new URL(await endSessionEndpoint());
  target.searchParams.set("id_token_hint", idToken);
  const redirectUri = postLogoutRedirectUri();
  if (redirectUri) target.searchParams.set("post_logout_redirect_uri", redirectUri);
  return NextResponse.json({ logoutUrl: target.toString() });
}

/**
 * Logout is POST-only so it can carry the same-origin CSRF check; a GET
 * logout would be triggerable by plain navigation or an embedded resource.
 */
export async function GET(_req: NextRequest) {
  return NextResponse.json({ error: "Method not allowed" }, { status: 405 });
}
