import NextAuth from "next-auth";
import KeycloakProvider from "next-auth/providers/keycloak";
import { requiredEnv, rejectInsecureInProd, isProduction } from "@/lib/env";
import {
  keycloakClientId,
  keycloakClientSecret,
  keycloakIssuer,
  type RefreshableToken,
} from "@/lib/keycloak";
import { SESSION_MAX_AGE_SECONDS, jwtCallback } from "@/lib/session";

/**
 * Auth.js v5 configuration.
 *
 * Env vars (v5 uses the AUTH_ prefix convention):
 *   AUTH_KEYCLOAK_ID          — Keycloak client ID
 *   AUTH_KEYCLOAK_SECRET      — Keycloak client secret
 *   AUTH_KEYCLOAK_ISSUER      — e.g. http://localhost:8081/realms/freecloud
 *   AUTH_SECRET               — used to sign the session cookie (was NEXTAUTH_SECRET)
 *   AUTH_URL                  — app base URL (was NEXTAUTH_URL); optional in prod
 *
 * The Keycloak vars fall back to dev defaults but throw in production if unset.
 * AUTH_SECRET is validated at runtime (skipped during `next build`) because
 * Auth.js uses it to sign the session cookie.
 */

// Validate at runtime — skipped during `next build` via env.isBuildPhase.
// Reject the example placeholder so a copied-from-example deploy can't ship
// with a publicly known session-signing secret.
const authSecret = requiredEnv("AUTH_SECRET");
rejectInsecureInProd("AUTH_SECRET", authSecret);

// M6: the Session type intentionally does NOT carry accessToken/idToken.
// Those live only on the server-side encrypted JWT (see the `jwt` callback
// below and TokenWithAccess) and are read server-side via next-auth/jwt's
// getToken() by the BFF proxy route (app/api/v1/[...path]/route.ts) — never
// copied onto the client-visible Session. Only the non-sensitive
// refresh-error flag is exposed, so the client can detect a broken session
// and force a re-login.
declare module "next-auth" {
  interface Session {
    error?: "RefreshAccessTokenError";
  }
}

// The JWT type from @auth/core extends Record<string, unknown>, so our custom
// fields are accepted but typed as `unknown`. This alias narrows them at the
// read sites inside the callbacks below.
type TokenWithAccess = RefreshableToken;

export const { handlers, auth, signIn, signOut } = NextAuth({
  providers: [
    KeycloakProvider({
      clientId: keycloakClientId,
      clientSecret: keycloakClientSecret,
      issuer: keycloakIssuer,
    }),
  ],
  session: {
    // JWT-backed sessions so the access token can be forwarded to the Go API.
    strategy: "jwt",
    // Explicit lifetime — see SESSION_MAX_AGE_SECONDS above.
    maxAge: SESSION_MAX_AGE_SECONDS,
  },
  // In production, force Secure cookies and the __Secure-/__Host- name prefixes
  // so the session cookie is never sent over plaintext HTTP. Left off in dev so
  // the cookie still works over http://localhost.
  useSecureCookies: isProduction(),
  pages: {
    signIn: "/signin",
    error: "/signin?error=auth_failed",
  },
  callbacks: {
    jwt: jwtCallback,
    async session({ session, token }) {
      // M6: deliberately do NOT copy accessToken/idToken from the JWT onto
      // the session — that would put the raw Keycloak token within reach of
      // any client-side XSS. Server-side code that needs the real token
      // (the BFF proxy route) reads it via next-auth/jwt's getToken()
      // instead, which never crosses into client-visible state.
      const t = token as typeof token & TokenWithAccess;
      session.error = t.error;
      return session;
    },
    async signIn({ account, profile }) {
      if (account?.provider === "keycloak") {
        return Boolean((profile as { sub?: string } | undefined)?.sub);
      }
      return true;
    },
  },
});
