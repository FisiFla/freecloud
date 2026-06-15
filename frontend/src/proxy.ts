import { auth } from "@/auth";

// Auth.js v5: the `auth` export doubles as proxy middleware when used as the default
// export. It protects all routes except the auth API, sign-in page, and static
// assets.
export default auth;

export const config = {
  matcher: ["/((?!api/auth|signin|_next/static|_next/image|favicon.ico).*)"],
};
