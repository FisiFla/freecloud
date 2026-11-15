/**
 * Writes an updated session JWT back to the Auth.js session cookie.
 *
 * The BFF proxy route refreshes an expired access token itself (the Auth.js
 * jwt callback only runs on session reads). Keycloak rotates the refresh
 * token on every exchange and the realm allows no reuse, so the rotated
 * token has to reach the cookie — otherwise the next refresh presents the
 * spent one and Keycloak revokes the session.
 *
 * Auth.js has no public API for this from a Route Handler, so the cookie is
 * written here following its conventions (name, salt, chunking). The
 * round-trip test reads the result back with next-auth/jwt's getToken(), so
 * a convention change in an Auth.js upgrade fails the test instead of
 * silently logging users out.
 */
import { encode, type JWT } from "next-auth/jwt";
import type { NextRequest, NextResponse } from "next/server";

/**
 * Session lifetime in seconds (8 hours).
 *
 * Auth.js defaults to 30 days with no idle timeout (see
 * @auth/core/lib/init.js), which is far too long for an admin dashboard
 * holding a live Keycloak access token. The value bounds both the session
 * JWT's `exp` and the session cookie's lifetime.
 */
export const SESSION_MAX_AGE_SECONDS = 8 * 60 * 60;

// Auth.js splits a session cookie larger than this into `<name>.<n>` chunks
// (@auth/core/lib/utils/cookie.js: 4096 minus an estimated 160 of overhead).
const CHUNK_SIZE = 4096 - 160;

export function sessionCookieName(secure: boolean): string {
  return secure ? "__Secure-authjs.session-token" : "authjs.session-token";
}

export async function persistSessionToken(
  req: NextRequest,
  res: NextResponse,
  token: JWT,
  secret: string,
  secure: boolean,
): Promise<void> {
  const name = sessionCookieName(secure);
  const value = await encode({
    token,
    secret,
    salt: name,
    maxAge: SESSION_MAX_AGE_SECONDS,
  });
  const options = {
    httpOnly: true,
    sameSite: "lax" as const,
    path: "/",
    secure,
  };

  const chunks: { name: string; value: string }[] = [];
  if (value.length <= CHUNK_SIZE) {
    chunks.push({ name, value });
  } else {
    for (let i = 0; i * CHUNK_SIZE < value.length; i++) {
      chunks.push({ name: `${name}.${i}`, value: value.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE) });
    }
  }

  // Drop chunks of the previous cookie that the new value no longer uses.
  const written = new Set(chunks.map((c) => c.name));
  for (const existing of req.cookies.getAll()) {
    const isSessionCookie = existing.name === name || existing.name.startsWith(`${name}.`);
    if (isSessionCookie && !written.has(existing.name)) {
      res.cookies.set(existing.name, "", { ...options, maxAge: 0 });
    }
  }
  const expires = new Date(Date.now() + SESSION_MAX_AGE_SECONDS * 1000);
  for (const chunk of chunks) {
    res.cookies.set(chunk.name, chunk.value, { ...options, expires });
  }
}
