import { describe, it, expect, afterEach } from "vitest";
import { GET } from "./route";

describe("version route (app/api/version)", () => {
  const origVersion = process.env.FREECLOUD_VERSION;
  const origSha = process.env.GIT_SHA;

  afterEach(() => {
    if (origVersion === undefined) delete process.env.FREECLOUD_VERSION;
    else process.env.FREECLOUD_VERSION = origVersion;
    if (origSha === undefined) delete process.env.GIT_SHA;
    else process.env.GIT_SHA = origSha;
  });

  it("reports the build-time version and commit from the environment", async () => {
    process.env.FREECLOUD_VERSION = "v1.2.3";
    process.env.GIT_SHA = "abc1234";
    const res = GET();
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "v1.2.3", commit: "abc1234" });
  });

  it("falls back to dev/unknown when the env vars are unset", async () => {
    delete process.env.FREECLOUD_VERSION;
    delete process.env.GIT_SHA;
    const res = GET();
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ version: "dev", commit: "unknown" });
  });
});
