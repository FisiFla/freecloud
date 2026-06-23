import { type NextRequest, NextResponse } from "next/server";
import { auth } from "@/auth";

const INTERNAL_API_URL = process.env.INTERNAL_API_URL ?? "http://localhost:8080";

// Routes that bypass all checks (public / auth plumbing / static assets).
const PUBLIC_PREFIXES = ["/signin", "/api/auth", "/forgot-password"];

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

export default async function middleware(request: NextRequest) {
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

  // For non-public paths: if the instance is not yet provisioned, redirect to /setup.
  if (!isPublicPath(pathname)) {
    const provisioned = await checkProvisioned();
    if (!provisioned) {
      return NextResponse.redirect(new URL("/setup", request.url));
    }
  }

  // Run the normal Auth.js authentication check.
  // Auth.js v5's `auth` export has a broad overloaded signature; cast via
  // unknown to the narrower middleware form we need.
  return (auth as unknown as (req: NextRequest) => Promise<NextResponse>)(request);
}

export const config = {
  matcher: ["/((?!api/auth|_next/static|_next/image|favicon.ico).*)"],
};
