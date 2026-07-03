import { NextResponse } from "next/server";
import { auth } from "@/auth";
import type { NextAuthRequest } from "next-auth";

const INTERNAL_API_URL = process.env.INTERNAL_API_URL ?? "http://localhost:8080";

// Routes that bypass all checks (public / auth plumbing / static assets).
// M5: /access-blocked must also be public — Keycloak's posture-check
// authenticator (a Keycloak SPI, not this app) redirects the browser here
// directly from within the login flow, before any Auth.js session cookie
// exists.
const PUBLIC_PREFIXES = ["/signin", "/api/auth", "/forgot-password", "/access-blocked"];

function isPublicPath(pathname: string): boolean {
  return PUBLIC_PREFIXES.some((p) => pathname === p || pathname.startsWith(p + "/"));
}

function isStaticPath(pathname: string): boolean {
  return pathname.startsWith("/_next/") || pathname === "/favicon.ico";
}

// checkProvisioned calls the backend setup-status endpoint.
// Fails open (returns true = provisioned) on network errors so that
// connectivity issues do not redirect every user to /setup.
async function checkProvisioned(): Promise<boolean> {
  try {
    const res = await fetch(`${INTERNAL_API_URL}/api/v1/setup/status`, {
      cache: "no-store",
    });
    if (!res.ok) return true;
    const json = (await res.json()) as {
      success: boolean;
      data?: { provisioned: boolean };
    };
    return json.data?.provisioned ?? true;
  } catch {
    // Network error — assume provisioned so existing deploys keep working.
    return true;
  }
}

// M5: previously this middleware called Auth.js's `auth` on its own
// (`auth(request)`) with no `authorized` callback configured anywhere, which
// is a no-op — it refreshed the session JWT but never actually redirected an
// unauthenticated request anywhere; every protected page rendered its own
// client-side "loading"/empty state instead of bouncing to /signin.
// Wrapping the handler AS `auth((req) => ...)` (rather than calling
// `auth(request)` from inside a plain function) is the documented Auth.js v5
// middleware pattern that populates `req.auth`, so the redirect can be
// enforced explicitly here alongside the existing provisioning-redirect
// logic. Note this route's matcher already excludes all of `/api/*` (Route
// Handlers, including the M6 BFF proxy under /api/v1, own their own auth
// responses — a redirect here would be the wrong shape for a fetch() caller).
export default auth(async (request: NextAuthRequest) => {
  const { pathname } = request.nextUrl;

  if (isStaticPath(pathname)) {
    return NextResponse.next();
  }

  // /setup path — if already provisioned, redirect to home.
  if (pathname === "/setup" || pathname.startsWith("/setup/")) {
    const provisioned = await checkProvisioned();
    if (provisioned) {
      return NextResponse.redirect(new URL("/", request.url));
    }
    return NextResponse.next();
  }

  if (isPublicPath(pathname)) {
    return NextResponse.next();
  }

  // Not yet provisioned — send everyone to /setup regardless of auth state.
  const provisioned = await checkProvisioned();
  if (!provisioned) {
    return NextResponse.redirect(new URL("/setup", request.url));
  }

  // The actual fix: no session → bounce to /signin instead of letting the
  // request fall through to the page.
  if (!request.auth) {
    const signInUrl = new URL("/signin", request.url);
    signInUrl.searchParams.set("callbackUrl", pathname);
    return NextResponse.redirect(signInUrl);
  }

  return NextResponse.next();
});

export const config = {
  // Exclude ALL of /api (not just /api/auth) — Route Handlers under /api/v1
  // (the M6 BFF proxy) and /api/auth (NextAuth's own routes) return their own
  // JSON/redirect responses and must not be intercepted by this page-oriented
  // middleware.
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico).*)"],
};
