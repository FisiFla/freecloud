import { NextResponse, type NextRequest } from "next/server";
import { getToken } from "next-auth/jwt";
import { isProduction } from "@/lib/env";

/**
 * M6 backend-for-frontend proxy.
 *
 * Why this exists: the raw Keycloak access token used to be copied onto the
 * client-visible NextAuth session (see auth.ts's `session` callback) so
 * `lib/api.ts` could read it in the browser and attach it as a Bearer
 * header directly from client JS. Any XSS on the dashboard would have been
 * able to steal that token. The `session` callback no longer exposes
 * accessToken/idToken at all — they live ONLY in the server-side encrypted
 * JWT cookie, which this Route Handler reads via `getToken()` (a mechanism
 * distinct from the client-facing `session`/`useSession()` APIs — it never
 * reaches browser-executable JS).
 *
 * Every authenticated call from `lib/api.ts` now hits this same-origin route
 * (e.g. `/api/v1/users`) instead of the Go backend directly. This handler
 * resolves the caller's session server-side, attaches the real Authorization
 * header, forwards to INTERNAL_API_URL (the backend's internal-network
 * address — see proxy.ts, which already uses the same var for the
 * setup-status check), and streams the response straight back. The browser
 * never sees the token.
 */

const INTERNAL_API_URL = process.env.INTERNAL_API_URL ?? "http://localhost:8080";

// Backend routes that are unauthenticated by design (see routes.go) and are
// still reached through lib/api.ts's request() helper — most notably
// forgot-password, which by definition must work for a user who cannot log
// in. Forwarded without requiring a session; the Authorization header is
// still attached if a session happens to be present.
const PUBLIC_BACKEND_PATHS = new Set([
  "/health",
  "/health/keycloak",
  "/health/fleetdm",
  "/auth/forgot-password",
  "/setup",
  "/setup/status",
]);

/** Max path segments under /api/v1 (defense against oversized BFF fan-out). */
const MAX_PATH_SEGMENTS = 16;

/** Reject path segments that could escape /api/v1 via .. or empty components. */
function sanitizePathParts(pathParts: string[]): string | null {
  if (pathParts.length === 0) return null;
  if (pathParts.length > MAX_PATH_SEGMENTS) return null;
  const decoded: string[] = [];
  for (const part of pathParts) {
    let seg: string;
    try {
      seg = decodeURIComponent(part);
    } catch {
      return null;
    }
    if (!seg || seg === "." || seg === ".." || seg.includes("/") || seg.includes("\\")) {
      return null;
    }
    // Reject control characters (incl. NUL) that can confuse logs/proxies.
    for (let i = 0; i < seg.length; i++) {
      const c = seg.charCodeAt(i);
      if (c < 0x20 || c === 0x7f) return null;
    }
    // Bound segment length so the BFF cannot be used to smuggle huge path blobs.
    if (seg.length > 256) {
      return null;
    }
    // Reject encoded-dot tricks that survived a single decode.
    const lower = seg.toLowerCase();
    if (lower === "%2e" || lower === "%2e%2e") return null;
    decoded.push(seg);
  }
  return "/" + decoded.map(encodeURIComponent).join("/");
}

async function proxy(req: NextRequest, pathParts: string[]): Promise<NextResponse> {
  const backendPath = sanitizePathParts(pathParts);
  if (backendPath === null) {
    return NextResponse.json({ success: false, error: "invalid path" }, { status: 400 });
  }
  const isPublic = PUBLIC_BACKEND_PATHS.has(backendPath);

  const token = await getToken({
    req,
    secret: process.env.AUTH_SECRET,
    secureCookie: isProduction(),
  });

  const accessToken = typeof token?.accessToken === "string" ? token.accessToken : undefined;
  const refreshFailed = token?.error === "RefreshAccessTokenError";

  if (!isPublic && (!accessToken || refreshFailed)) {
    return NextResponse.json({ success: false, error: "unauthorized" }, { status: 401 });
  }

  const base = INTERNAL_API_URL.replace(/\/$/, "");
  const targetUrl = new URL(`${base}/api/v1${backendPath}`);
  // Defense in depth: after URL resolution the pathname must still be under /api/v1.
  if (!targetUrl.pathname.startsWith("/api/v1/") && targetUrl.pathname !== "/api/v1") {
    return NextResponse.json({ success: false, error: "invalid path" }, { status: 400 });
  }
  targetUrl.search = req.nextUrl.search;

  const outHeaders = new Headers();
  if (accessToken && !refreshFailed) {
    outHeaders.set("Authorization", `Bearer ${accessToken}`);
  }
  const orgId = req.headers.get("x-org-id");
  if (orgId) outHeaders.set("X-Org-Id", orgId);
  const contentType = req.headers.get("content-type");
  if (contentType) outHeaders.set("Content-Type", contentType);

  const hasBody = !["GET", "HEAD"].includes(req.method);

  let backendRes: Response;
  try {
    backendRes = await fetch(targetUrl.toString(), {
      method: req.method,
      headers: outHeaders,
      body: hasBody ? await req.arrayBuffer() : undefined,
      // @ts-expect-error -- undici-only option, required when forwarding a body.
      duplex: hasBody ? "half" : undefined,
    });
  } catch {
    return NextResponse.json({ success: false, error: "backend unreachable" }, { status: 502 });
  }

  const buf = await backendRes.arrayBuffer();
  const resHeaders = new Headers();
  const respContentType = backendRes.headers.get("content-type");
  if (respContentType) resHeaders.set("content-type", respContentType);
  const contentDisposition = backendRes.headers.get("content-disposition");
  if (contentDisposition) resHeaders.set("content-disposition", contentDisposition);

  return new NextResponse(buf, { status: backendRes.status, headers: resHeaders });
}

type RouteContext = { params: Promise<{ path: string[] }> };

export async function GET(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
export async function POST(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
export async function PUT(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
export async function PATCH(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
export async function DELETE(req: NextRequest, ctx: RouteContext) {
  const { path } = await ctx.params;
  return proxy(req, path);
}
