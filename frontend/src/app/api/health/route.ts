export const dynamic = "force-dynamic";

/** Process liveness only — no auth, no backend call. */
export function GET() {
  return Response.json({ status: "ok" });
}
