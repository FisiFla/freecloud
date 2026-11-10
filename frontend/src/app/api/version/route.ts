export const dynamic = "force-dynamic";

/** Build identity, injected at image build time (see frontend/Dockerfile). */
export function GET() {
  return Response.json({
    version: process.env.FREECLOUD_VERSION || "dev",
    commit: process.env.GIT_SHA || "unknown",
  });
}
