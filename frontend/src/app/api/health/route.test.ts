import { describe, it, expect } from "vitest";
import { GET } from "./route";

describe("health route (app/api/health)", () => {
  it("answers 200 with {status: ok} without any backend call", async () => {
    const res = GET();
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ status: "ok" });
  });
});
