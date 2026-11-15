import { type NextRequest } from "next/server";
import { getToken } from "next-auth/jwt";
import { isProduction } from "@/lib/env";

export const dynamic = "force-dynamic";

/**
 * Build identity, injected at image build time (see frontend/Dockerfile).
 *
 * The version stays public — scripts/prod-smoke.sh asserts it
 * unauthenticated — but the exact commit hash is only revealed to
 * authenticated sessions, so an anonymous scanner cannot pin the running
 * build to a source revision.
 */
export async function GET(req: NextRequest) {
  let authenticated = false;
  try {
    const token = await getToken({
      req,
      secret: process.env.AUTH_SECRET,
      secureCookie: isProduction(),
    });
    authenticated = !!token;
  } catch {
    // An unreadable session cookie is simply an unauthenticated request.
    authenticated = false;
  }
  const version = process.env.FREECLOUD_VERSION || "dev";
  if (!authenticated) {
    return Response.json({ version });
  }
  return Response.json({ version, commit: process.env.GIT_SHA || "unknown" });
}
