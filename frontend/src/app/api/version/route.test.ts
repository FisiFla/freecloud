import { describe, it, expect, afterEach, vi } from "vitest";
import { NextRequest } from "next/server";

const { getTokenMock } = vi.hoisted(() => ({ getTokenMock: vi.fn() }));
vi.mock("next-auth/jwt", () => ({ getToken: getTokenMock }));

import { GET } from "./route";

function makeRequest(): NextRequest {
  return new NextRequest(new Request("http://localhost/api/version"));
}

describe("version route (app/api/version)", () => {
  const origVersion = process.env.FREECLOUD_VERSION;
  const origSha = process.env.GIT_SHA;

  afterEach(() => {
    if (origVersion === undefined) delete process.env.FREECLOUD_VERSION;
    else process.env.FREECLOUD_VERSION = origVersion;
    if (origSha === undefined) delete process.env.GIT_SHA;
    else process.env.GIT_SHA = origSha;
    getTokenMock.mockReset();
  });

  it("returns only the version to unauthenticated callers (commit stays hidden)", async () => {
    process.env.FREECLOUD_VERSION = "v1.2.3";
    process.env.GIT_SHA = "abc1234";
    getTokenMock.mockResolvedValue(null);
    const res = await GET(makeRequest());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "v1.2.3" });
  });

  it("returns the version and commit to authenticated sessions", async () => {
    process.env.FREECLOUD_VERSION = "v1.2.3";
    process.env.GIT_SHA = "abc1234";
    getTokenMock.mockResolvedValue({ sub: "u1" });
    const res = await GET(makeRequest());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "v1.2.3", commit: "abc1234" });
  });

  it("hides the commit when the session token cannot be read", async () => {
    process.env.FREECLOUD_VERSION = "v1.2.3";
    process.env.GIT_SHA = "abc1234";
    getTokenMock.mockRejectedValue(new Error("bad cookie"));
    const res = await GET(makeRequest());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "v1.2.3" });
  });

  it("falls back to dev/unknown when the env vars are unset", async () => {
    delete process.env.FREECLOUD_VERSION;
    delete process.env.GIT_SHA;
    getTokenMock.mockResolvedValue({ sub: "u1" });
    const res = await GET(makeRequest());
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "dev", commit: "unknown" });
  });
});
