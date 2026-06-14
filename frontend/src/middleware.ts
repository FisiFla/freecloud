import { auth } from "@/auth";

// Auth.js v5: the `auth` export doubles as middleware when used as the default
// export. It protects all routes except the auth API, sign-in page, and static
// assets.
export default auth;

export const config = {
  // Force the Node.js runtime. By default Next.js middleware runs on the Edge
  // runtime, but Auth.js v5 pulls in `jose` which uses CompressionStream /
  // DecompressionStream — APIs that emit warnings (or are unavailable) on older
  // Edge runtimes. We don't need Edge's global distribution for this auth
  // guard, so pinning to Node.js removes the warning and the compatibility risk.
  runtime: "nodejs",
  matcher: ["/((?!api/auth|signin|_next/static|_next/image|favicon.ico).*)"],
};
