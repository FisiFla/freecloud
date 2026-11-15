import { describe, it, expect } from "vitest";
import { NextRequest, NextResponse } from "next/server";
import { getToken } from "next-auth/jwt";
import { persistSessionToken, sessionCookieName } from "./session-cookie";

const SECRET = "test-secret-test-secret-test-secret-test";

/** Replays the cookies a response set onto a new request, as a browser would. */
function followUp(res: NextResponse, carried: Record<string, string> = {}): NextRequest {
  const jar = { ...carried };
  for (const c of res.cookies.getAll()) {
    if (c.value === "") delete jar[c.name];
    else jar[c.name] = c.value;
  }
  const header = Object.entries(jar)
    .map(([k, v]) => `${k}=${v}`)
    .join("; ");
  return new NextRequest("https://app.example.com/api/v1/users", { headers: { cookie: header } });
}

describe("persistSessionToken", () => {
  for (const secure of [true, false]) {
    it(`round-trips through getToken (secure=${secure})`, async () => {
      const req = new NextRequest("https://app.example.com/api/v1/users");
      const res = NextResponse.json({});
      await persistSessionToken(
        req,
        res,
        { sub: "u1", accessToken: "at", refreshToken: "rotated-rt", expiresAt: 123 },
        SECRET,
        secure,
      );

      const cookie = res.cookies.get(sessionCookieName(secure));
      expect(cookie?.httpOnly).toBe(true);
      expect(cookie?.sameSite).toBe("lax");
      expect(cookie?.secure).toBe(secure);

      const token = await getToken({ req: followUp(res), secret: SECRET, secureCookie: secure });
      expect(token?.refreshToken).toBe("rotated-rt");
      expect(token?.accessToken).toBe("at");
      expect(token?.sub).toBe("u1");
    });
  }

  it("chunks a large session and clears chunks the new value no longer uses", async () => {
    const name = sessionCookieName(true);
    const big = { sub: "u1", accessToken: "a".repeat(9000), refreshToken: "rt-1" };
    const res1 = NextResponse.json({});
    await persistSessionToken(
      new NextRequest("https://app.example.com/"),
      res1,
      big,
      SECRET,
      true,
    );
    const names1 = res1.cookies.getAll().map((c) => c.name);
    expect(names1.length).toBeGreaterThan(1);
    expect(names1).toContain(`${name}.0`);

    const req2 = followUp(res1);
    const token = await getToken({ req: req2, secret: SECRET, secureCookie: true });
    expect(token?.accessToken).toBe(big.accessToken);

    // A smaller session afterwards must not leave stale chunks behind.
    const res2 = NextResponse.json({});
    await persistSessionToken(req2, res2, { sub: "u1", refreshToken: "rt-2" }, SECRET, true);
    const carried = Object.fromEntries(req2.cookies.getAll().map((c) => [c.name, c.value]));
    const req3 = followUp(res2, carried);
    expect(req3.cookies.getAll().map((c) => c.name)).toEqual([name]);
    const small = await getToken({ req: req3, secret: SECRET, secureCookie: true });
    expect(small?.refreshToken).toBe("rt-2");
  });
});
