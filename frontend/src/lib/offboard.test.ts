import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

/**
 * Smoke: privileged offboard path goes through the same-origin BFF and
 * surfaces failures as ApiError — not a silent success.
 */
describe("offboardUser (privileged API)", () => {
  const origFetch = global.fetch;

  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    global.fetch = origFetch;
  });

  it("POSTs to /api/v1/offboard/:id and returns OffboardResponse data", async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          success: true,
          data: {
            userId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
            sessionsTerminated: true,
            devicesWiped: 1,
            devicesFailed: 0,
          },
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    global.fetch = fetchSpy as unknown as typeof fetch;

    const { offboardUser } = await import("./api");
    const res = await offboardUser("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
    expect(res.userId).toBe("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
    expect(res.sessionsTerminated).toBe(true);
    expect(res.devicesWiped).toBe(1);

    expect(fetchSpy).toHaveBeenCalled();
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toContain("/api/v1/offboard/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
    expect((init as RequestInit).method).toBe("POST");
  });

  it("throws ApiError when backend returns non-success", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ success: false, error: "forbidden: insufficient permissions" }), {
        status: 403,
        headers: { "content-type": "application/json" },
      }),
    ) as unknown as typeof fetch;

    const { offboardUser, ApiError } = await import("./api");
    await expect(offboardUser("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")).rejects.toBeInstanceOf(ApiError);
  });
});
