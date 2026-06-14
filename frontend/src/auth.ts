import NextAuth from "next-auth";
import KeycloakProvider from "next-auth/providers/keycloak";
import { requiredProdEnv, requiredEnv, rejectInsecureInProd, isProduction } from "@/lib/env";

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

const keycloakClientId = requiredProdEnv("AUTH_KEYCLOAK_ID", "freecloud-dashboard");
const keycloakClientSecret = requiredProdEnv("AUTH_KEYCLOAK_SECRET", "");
const keycloakIssuer = requiredProdEnv(
  "AUTH_KEYCLOAK_ISSUER",
  "http://localhost:8081/realms/freecloud",
);

// Augment the Session type so the access token and refresh-error flag are
// visible to client/server consumers without `as any` casts.
declare module "next-auth" {
  interface Session {
    accessToken?: string;
    idToken?: string;
    error?: "RefreshAccessTokenError";
  }
}

// The JWT type from @auth/core extends Record<string, unknown>, so our custom
// fields are accepted but typed as `unknown`. This alias narrows them at the
// read sites inside the callbacks below.
type TokenWithAccess = {
  accessToken?: string;
  idToken?: string;
  refreshToken?: string;
  expiresAt?: number;
  error?: "RefreshAccessTokenError";
};

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
    async jwt({ token, account }) {
      const t = token as typeof token & TokenWithAccess;

      // Initial sign-in: persist the OIDC tokens on the JWT.
      if (account) {
        t.accessToken = account.access_token;
        t.idToken = account.id_token;
        t.refreshToken = account.refresh_token;
        t.expiresAt = Math.floor(Date.now() / 1000) + (account.expires_in ?? 0);
        return t;
      }

      // Token still valid for more than 30s — keep it.
      if (t.expiresAt && Date.now() / 1000 < t.expiresAt - 30) {
        return t;
      }

      // About to expire — refresh via Keycloak's token endpoint.
      try {
        const res = await fetch(`${keycloakIssuer}/protocol/openid-connect/token`, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            client_id: keycloakClientId,
            client_secret: keycloakClientSecret,
            grant_type: "refresh_token",
            refresh_token: t.refreshToken ?? "",
          }),
        });

        if (!res.ok) {
          t.error = "RefreshAccessTokenError";
          return t;
        }

        const tokens = (await res.json()) as {
          access_token?: string;
          id_token?: string;
          refresh_token?: string;
          expires_in?: number;
        };
        t.accessToken = tokens.access_token;
        t.idToken = tokens.id_token ?? t.idToken;
        t.refreshToken = tokens.refresh_token ?? t.refreshToken;
        t.expiresAt = Math.floor(Date.now() / 1000) + (tokens.expires_in ?? 0);
      } catch {
        t.error = "RefreshAccessTokenError";
      }

      return t;
    },
    async session({ session, token }) {
      const t = token as typeof token & TokenWithAccess;
      session.accessToken = t.accessToken;
      session.idToken = t.idToken;
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
