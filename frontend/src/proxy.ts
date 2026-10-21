import { NextResponse } from "next/server";
import { auth } from "@/auth";
import type { NextAuthRequest } from "next-auth";
import { requiredProdEnv } from "@/lib/env";

// Fail closed in production: a missing INTERNAL_API_URL used to silently
// fall back to http://localhost:8080, which inside the frontend container
// resolves to nothing — every BFF call then failed with ECONNREFUSED and a
// 502 "backend unreachable" with no obvious cause. requiredProdEnv throws at
// runtime instead, so a misconfigured deployment refuses to serve.
const INTERNAL_API_URL = requiredProdEnv("INTERNAL_API_URL", "http://localhost:8080");

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

// Nonce-based CSP (see next.config.js comment: "tighten to nonces in a later
// pass" — this is that pass). The policy builder lives in src/lib/csp.ts
// (dependency-free, unit-tested); the nonce is generated below and forwarded
// to layout.tsx on the request headers.
import { buildCsp } from "@/lib/csp";

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

  // Nonce-based CSP for every HTML page response. The nonce is forwarded to
  // the app on the REQUEST headers (see nextWithNonce below) so layout.tsx
  // can apply it to the inline dark-mode script.
  const nonce = crypto.randomUUID();
  const cspResponse = (res: NextResponse): NextResponse => {
    res.headers.set("Content-Security-Policy", buildCsp(nonce));
    return res;
  };

  // Forward the nonce to the app on the REQUEST headers.
  //
  // Next.js only propagates a middleware-set request header downstream when
  // the response is built with `NextResponse.next({ request: { headers } })`
  // — that is what makes Next set the internal x-middleware-override-headers
  // list. Setting the header on the RESPONSE alone never reaches the
  // layout, which reads it from `headers()`; the nonce-based CSP was
  // therefore dead code and the browser blocked the app's inline
  // dark-mode script.
  //
  // The nonce is deliberately NOT echoed back as a response header — that
  // would expose it to anything able to read response headers.
  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-nonce", nonce);
  const nextWithNonce = (): NextResponse =>
    cspResponse(NextResponse.next({ request: { headers: requestHeaders } }));

  // /setup path — if already provisioned, redirect to home.
  if (pathname === "/setup" || pathname.startsWith("/setup/")) {
    const provisioned = await checkProvisioned();
    if (provisioned) {
      return cspResponse(NextResponse.redirect(new URL("/", request.url)));
    }
    return nextWithNonce();
  }

  if (isPublicPath(pathname)) {
    return nextWithNonce();
  }

  // Not yet provisioned — send everyone to /setup regardless of auth state.
  const provisioned = await checkProvisioned();
  if (!provisioned) {
    return cspResponse(NextResponse.redirect(new URL("/setup", request.url)));
  }

  // The actual fix: no session → bounce to /signin instead of letting the
  // request fall through to the page.
  if (!request.auth) {
    const signInUrl = new URL("/signin", request.url);
    signInUrl.searchParams.set("callbackUrl", pathname);
    return cspResponse(NextResponse.redirect(signInUrl));
  }

  return nextWithNonce();
});

export const config = {
  // Exclude ALL of /api (not just /api/auth) — Route Handlers under /api/v1
  // (the M6 BFF proxy) and /api/auth (NextAuth's own routes) return their own
  // JSON/redirect responses and must not be intercepted by this page-oriented
  // middleware.
  matcher: ["/((?!api|_next/static|_next/image|favicon.ico).*)"],
};
