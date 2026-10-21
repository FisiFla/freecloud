import { NextResponse, type NextRequest } from "next/server";
import { getToken } from "next-auth/jwt";
import { isProduction, requiredProdEnv } from "@/lib/env";
import { refreshAccessToken } from "@/lib/keycloak";

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

/**
 * Fail closed in production: a missing INTERNAL_API_URL used to silently
 * fall back to http://localhost:8080, which inside the frontend container
 * resolves to nothing — every BFF call then failed with ECONNREFUSED and a
 * 502 "backend unreachable" with no obvious cause in the logs. requiredProdEnv
 * throws at runtime instead, so a misconfigured deployment refuses to serve.
 */
const INTERNAL_API_URL = requiredProdEnv("INTERNAL_API_URL", "http://localhost:8080");

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

/**
 * Max response body the BFF will buffer from the backend. Software/compliance
 * payloads can be large, but unbounded buffering lets a pathological (or
 * compromised) backend balloon the BFF's memory. 50 MiB is far beyond any
 * legitimate payload while keeping the proxy bounded. Operators can tune via
 * BFF_MAX_RESPONSE_BYTES (bytes).
 */
const MAX_BFF_RESPONSE_BYTES = 50 * 1024 * 1024;

/**
 * Max request body the BFF accepts from the browser. The response side is
 * capped, but the request side used to be buffered with no limit at all — a
 * single oversized POST could exhaust the Node process's memory. 10 MiB is far
 * beyond any legitimate payload (the largest is the bulk-onboarding CSV) while
 * keeping the proxy bounded. Operators can tune via BFF_MAX_REQUEST_BYTES.
 */
const MAX_BFF_REQUEST_BYTES = 10 * 1024 * 1024;

/**
 * Upstream timeout for a single backend call. Without it, a backend that
 * accepts the TCP connection and then stalls (stuck Postgres query, hung
 * FleetDM call) pins a Next.js worker open forever. Operators can tune via
 * BFF_TIMEOUT_MS (milliseconds).
 */
const DEFAULT_BFF_TIMEOUT_MS = 30_000;

/** Methods that change state and therefore need a CSRF check. */
const STATE_CHANGING_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/** Returns the effective BFF response cap, honoring the env override. */
export function getBffMaxResponseBytes(): number {
  const raw = process.env.BFF_MAX_RESPONSE_BYTES;
  if (raw) {
    const n = Number(raw);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
  }
  return MAX_BFF_RESPONSE_BYTES;
}

/** Returns the effective BFF request-body cap, honoring the env override. */
export function getBffMaxRequestBytes(): number {
  const raw = process.env.BFF_MAX_REQUEST_BYTES;
  if (raw) {
    const n = Number(raw);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
  }
  return MAX_BFF_REQUEST_BYTES;
}

/** Returns the effective upstream timeout, honoring the env override. */
export function getBffTimeoutMs(): number {
  const raw = process.env.BFF_TIMEOUT_MS;
  if (raw) {
    const n = Number(raw);
    if (Number.isFinite(n) && n > 0) return Math.floor(n);
  }
  return DEFAULT_BFF_TIMEOUT_MS;
}

/** Reads a backend response body, aborting at the byte limit (default 50 MiB). */
export async function readBoundedBody(
  res: Response,
  limit: number = MAX_BFF_RESPONSE_BYTES,
): Promise<{ ok: true; bytes: ArrayBuffer } | { ok: false }> {
  if (!res.body) {
    return { ok: true, bytes: new ArrayBuffer(0) };
  }
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > limit) {
      await reader.cancel().catch(() => {});
      return { ok: false };
    }
    chunks.push(value);
  }
  const merged = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    merged.set(c, off);
    off += c.byteLength;
  }
  return { ok: true, bytes: merged.buffer };
}

/**
 * Wraps the incoming request body in a byte-counting stream so an oversized
 * upload is aborted mid-transfer instead of being buffered in the BFF's
 * memory. `exceeded()` reports whether the limit was hit, so the caller can
 * answer 413 instead of 502.
 */
export function boundedBodyStream(
  body: ReadableStream<Uint8Array>,
  limit: number,
): { stream: ReadableStream<Uint8Array>; exceeded: () => boolean } {
  const reader = body.getReader();
  let total = 0;
  let exceeded = false;
  const stream = new ReadableStream<Uint8Array>({
    async pull(controller) {
      const { done, value } = await reader.read();
      if (done) {
        controller.close();
        return;
      }
      total += value.byteLength;
      if (total > limit) {
        exceeded = true;
        controller.error(new Error("BFF request body limit exceeded"));
        await reader.cancel().catch(() => {});
        return;
      }
      controller.enqueue(value);
    },
    cancel(reason) {
      return reader.cancel(reason);
    },
  });
  return { stream, exceeded: () => exceeded };
}

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
    if (!seg || seg === "." || seg === ".." || seg === "..." || seg.includes("/") || seg.includes("\\")) {
      return null;
    }
    // Reject control characters (incl. NUL) that can confuse logs/proxies.
    for (let i = 0; i < seg.length; i++) {
      const c = seg.charCodeAt(i);
      if (c < 0x20 || c === 0x7f) return null;
    }
    // Reject whitespace-only or leading/trailing space segments after decode.
    if (seg !== seg.trim()) {
      return null;
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
 * CSRF guard for state-changing BFF requests.
 *
 * The session cookie is SameSite=Lax, which blocks classic cross-site form
 * POSTs but still lets a sibling subdomain issue credentialed fetch calls
 * against this app's origin — SameSite is about "site", not "origin". A
 * Content-Type of text/plain keeps such a request CORS-simple (no preflight)
 * and Go's JSON decoder ignores the declared content type, so the privileged
 * action used to execute.
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

/** True when the failure was the upstream timeout. */
function isTimeoutError(err: unknown, signal: AbortSignal): boolean {
  if (signal.aborted) return true;
  const name = err instanceof Error ? err.name : "";
  return name === "TimeoutError" || name === "AbortError";
}

/**
 * Replaces a backend error body with a generic, client-safe envelope.
 *
 * The Go handlers put `err.Error()` into the envelope's `error` field in
 * dozens of places, so a SQL error, a file path or an upstream URL used to
 * reach the browser verbatim and be rendered by ErrorBanner on every page.
 * The detail is logged server-side instead; the client only learns the
 * status code. Field-level validation messages (the structured `errors`
 * array) are kept — those are deliberate, user-facing messages.
 */
export function sanitizeErrorBody(
  buf: ArrayBuffer,
  status: number,
): { body: ArrayBuffer; contentType: string } {
  const text = new TextDecoder().decode(buf);
  const generic = `Request failed with status ${status}`;
  const encode = (value: string): ArrayBuffer =>
    new TextEncoder().encode(value).buffer as ArrayBuffer;
  // Log the raw detail server-side so operators keep the diagnostics.
  console.error(`[bff] backend error ${status}: ${text.slice(0, 2000)}`);
  try {
    const json = JSON.parse(text) as {
      success?: boolean;
      error?: string;
      errors?: unknown;
    };
    if (json && typeof json === "object" && !Array.isArray(json)) {
      if (typeof json.error === "string") {
        json.error = generic;
      } else if (!json.errors) {
        json.error = generic;
      }
      return {
        body: encode(JSON.stringify(json)),
        contentType: "application/json",
      };
    }
  } catch {
    // Not a JSON envelope — fall through to the generic body.
  }
  return {
    body: encode(JSON.stringify({ success: false, error: generic })),
    contentType: "application/json",
  };
}

async function proxy(req: NextRequest, pathParts: string[]): Promise<NextResponse> {
  const backendPath = sanitizePathParts(pathParts);
  if (backendPath === null) {
    return NextResponse.json({ success: false, error: "invalid path" }, { status: 400 });
  }
  const isPublic = PUBLIC_BACKEND_PATHS.has(backendPath);

  // CSRF: a state-changing request that did not come from this app's own
  // origin is rejected before it can touch the backend.
  if (isCrossSiteRequest(req)) {
    return NextResponse.json({ success: false, error: "forbidden" }, { status: 403 });
  }

  let token: Awaited<ReturnType<typeof getToken>> = null;
  try {
    token = await getToken({
      req,
      secret: process.env.AUTH_SECRET,
      secureCookie: isProduction(),
    });
  } catch {
    // A malformed Authorization header (e.g. "Bearer %") used to make
    // next-auth's getToken() throw a URIError, which surfaced as a 500 on
    // the only data path the dashboard has. A malformed credential is simply
    // an unauthenticated request.
    token = null;
  }

  let accessToken = typeof token?.accessToken === "string" ? token.accessToken : undefined;
  let refreshFailed = token?.error === "RefreshAccessTokenError";

  // getToken() only decrypts the session cookie — the jwt callback that
  // refreshes an expired access token runs when the session is read (via
  // /api/auth/session or the middleware). An access token that expired since
  // the last session read used to be forwarded as-is and the backend answered
  // 401, so refresh it here before forwarding.
  const expiresAt = typeof token?.expiresAt === "number" ? token.expiresAt : undefined;
  const nowSec = Math.floor(Date.now() / 1000);
  const refreshTokenValue =
    typeof token?.refreshToken === "string" ? token.refreshToken : undefined;
  const isExpired = expiresAt !== undefined && nowSec >= expiresAt;

  // A stored refresh-error flag is not on its own a reason to reject the
  // request: when the access token is known to still be valid, the flag is
  // stale (the last refresh attempt failed, but the token has not expired
  // yet). When the expiry is unknown, the safe answer is to reject.
  if (refreshFailed && accessToken && expiresAt !== undefined && !isExpired) {
    refreshFailed = false;
  }

  if (accessToken && refreshTokenValue && expiresAt !== undefined && nowSec >= expiresAt - 30) {
    const refreshed = await refreshAccessToken({
      accessToken,
      idToken: typeof token?.idToken === "string" ? token.idToken : undefined,
      refreshToken: refreshTokenValue,
      expiresAt,
    });
    if (!refreshed.error && refreshed.accessToken) {
      accessToken = refreshed.accessToken;
      // The refresh worked — the stored failure flag is stale.
      refreshFailed = false;
    } else if (nowSec >= expiresAt) {
      // The token is expired and cannot be refreshed — the session is dead.
      refreshFailed = true;
    }
  }

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

  // Reject an oversized request before reading (and forwarding) its body.
  const maxRequestBytes = getBffMaxRequestBytes();
  let bodyStream: ReadableStream<Uint8Array> | undefined;
  let bodyExceeded: (() => boolean) | null = null;
  if (hasBody) {
    const declaredLength = req.headers.get("content-length");
    if (declaredLength !== null) {
      const length = Number(declaredLength);
      if (Number.isFinite(length) && length > maxRequestBytes) {
        return NextResponse.json(
          { success: false, error: "request body too large" },
          { status: 413 },
        );
      }
    }
    if (req.body) {
      const bounded = boundedBodyStream(req.body, maxRequestBytes);
      bodyStream = bounded.stream;
      bodyExceeded = bounded.exceeded;
    }
  }

  const timeoutSignal = AbortSignal.timeout(getBffTimeoutMs());

  let backendRes: Response;
  try {
    backendRes = await fetch(targetUrl.toString(), {
      method: req.method,
      headers: outHeaders,
      body: bodyStream,
      // @ts-expect-error -- undici-only option, required when forwarding a body.
      duplex: hasBody ? "half" : undefined,
      signal: timeoutSignal,
    });
  } catch (err) {
    if (bodyExceeded && bodyExceeded()) {
      return NextResponse.json(
        { success: false, error: "request body too large" },
        { status: 413 },
      );
    }
    if (isTimeoutError(err, timeoutSignal)) {
      return NextResponse.json({ success: false, error: "backend timeout" }, { status: 504 });
    }
    return NextResponse.json({ success: false, error: "backend unreachable" }, { status: 502 });
  }

  const body = await readBoundedBody(backendRes, getBffMaxResponseBytes());
  if (!body.ok) {
    return NextResponse.json(
      { success: false, error: "backend response too large" },
      { status: 502 },
    );
  }
  const buf = body.bytes;
  const resHeaders = new Headers();
  const respContentType = backendRes.headers.get("content-type");
  if (respContentType) resHeaders.set("content-type", respContentType);
  const contentDisposition = backendRes.headers.get("content-disposition");
  if (contentDisposition) resHeaders.set("content-disposition", contentDisposition);

  if (backendRes.status >= 400) {
    // Never forward a raw upstream error body to the browser.
    const sanitized = sanitizeErrorBody(buf, backendRes.status);
    resHeaders.set("content-type", sanitized.contentType);
    return new NextResponse(sanitized.body, {
      status: backendRes.status,
      headers: resHeaders,
    });
  }

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
