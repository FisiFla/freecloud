/**
 * Same-origin CSRF guard for state-changing API routes.
 *
 * Shared by the BFF proxy (app/api/v1/[...path]) and the federated-logout
 * route (app/api/auth/federated-logout) so both enforce exactly the same
 * policy: a request the browser labels cross-site (Sec-Fetch-Site) or whose
 * Origin is not this app's own origin is rejected.
 */
import { type NextRequest } from "next/server";
import { isProduction } from "./env";

/** Methods that change state and therefore need a CSRF check. */
const STATE_CHANGING_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/**
 * True when `origin` is this app's own origin.
 *
 * The comparison is on the HOST rather than on the full origin: behind a
 * reverse proxy the request URL Next.js hands to a route handler can carry
 * the internal host, and the forwarded scheme is not always visible here.
 * The host is what separates this app from an attacker's sibling subdomain.
 * The allowed hosts are the request's own host, the forwarded host and the
 * configured AUTH_URL.
 */
function isAllowedOrigin(req: NextRequest, origin: string): boolean {
  let originUrl: URL;
  try {
    originUrl = new URL(origin);
  } catch {
    // "null" (sandboxed iframe) or a malformed Origin — never our own origin.
    return false;
  }
  const allowed = new Set<string>();
  const addHost = (value: string | null | undefined) => {
    if (!value) return;
    try {
      allowed.add(new URL(value).host);
    } catch {
      // Not a URL — ignore it.
    }
  };
  addHost(`http://${req.nextUrl.host}`);
  addHost(process.env.AUTH_URL);
  addHost(process.env.NEXTAUTH_URL);
  const forwardedHost = req.headers.get("x-forwarded-host");
  if (forwardedHost) {
    const proto =
      req.headers.get("x-forwarded-proto") || (isProduction() ? "https" : "http");
    addHost(`${proto}://${forwardedHost}`);
  }
  return allowed.has(originUrl.host);
}

/**
 * CSRF guard for state-changing requests.
 *
 * The session cookie is SameSite=Lax, which blocks classic cross-site form
 * POSTs but still lets a sibling subdomain issue credentialed fetch calls
 * against this app's origin — SameSite is about "site", not "origin". A
 * Content-Type of text/plain keeps such a request CORS-simple (no preflight)
 * and Go's JSON decoder ignores the declared content type, so a privileged
 * action used to execute (and a forced logout is one click away without this
 * check).
 *
 * Two independent browser signals are checked:
 *  - `Sec-Fetch-Site: cross-site` — sent by every modern browser.
 *  - `Origin` — sent by every browser on POST/PUT/PATCH/DELETE; it must be
 *    this app's own origin.
 *
 * A request with neither header cannot have been issued by a browser
 * cross-site, so it is allowed through and server-to-server clients keep
 * working.
 */
export function isCrossSiteRequest(req: NextRequest): boolean {
  if (!STATE_CHANGING_METHODS.has(req.method.toUpperCase())) return false;
  const secFetchSite = req.headers.get("sec-fetch-site");
  if (secFetchSite === "cross-site") return true;
  const origin = req.headers.get("origin");
  if (!origin) return false;
  return !isAllowedOrigin(req, origin);
}
