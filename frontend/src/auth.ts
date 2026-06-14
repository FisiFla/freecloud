import NextAuth from "next-auth";
import KeycloakProvider from "next-auth/providers/keycloak";

/**
 * Auth.js v5 configuration.
 *
 * Env vars (v5 uses the AUTH_ prefix convention):
 *   AUTH_KEYCLOAK_ID          — Keycloak client ID
 *   AUTH_KEYCLOAK_SECRET      — Keycloak client secret
 *   AUTH_KEYCLOAK_ISSUER      — e.g. http://localhost:8081/realms/freecloud
 *   AUTH_SECRET               — used to sign the session cookie (was NEXTAUTH_SECRET)
 *   AUTH_URL                  — app base URL (was NEXTAUTH_URL); optional in prod
 */

export const { handlers, auth, signIn, signOut } = NextAuth({
  providers: [
    KeycloakProvider({
      clientId: process.env.AUTH_KEYCLOAK_ID || "freecloud-dashboard",
      clientSecret: process.env.AUTH_KEYCLOAK_SECRET || "",
      issuer: process.env.AUTH_KEYCLOAK_ISSUER || "http://localhost:8081/realms/freecloud",
    }),
  ],
  session: {
    // JWT-backed sessions so the access token can be forwarded to the Go API.
    strategy: "jwt",
  },
  pages: {
    signIn: "/signin",
    error: "/signin?error=auth_failed",
  },
  callbacks: {
    async jwt({ token, account }) {
      type AugmentedToken = typeof token & {
        accessToken?: string;
        idToken?: string;
        refreshToken?: string;
        expiresAt?: number;
        error?: "RefreshAccessTokenError";
      };
      const t = token as AugmentedToken;

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
      const issuer =
        process.env.AUTH_KEYCLOAK_ISSUER || "http://localhost:8081/realms/freecloud";
      try {
        const res = await fetch(`${issuer}/protocol/openid-connect/token`, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            client_id: process.env.AUTH_KEYCLOAK_ID || "freecloud-dashboard",
            client_secret: process.env.AUTH_KEYCLOAK_SECRET || "",
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
      type AugmentedToken = typeof token & {
        accessToken?: string;
        idToken?: string;
        error?: "RefreshAccessTokenError";
      };
      const t = token as AugmentedToken;
      // Surface the access token and any refresh error on the session object.
      session.accessToken = t.accessToken;
      (session as typeof session & { idToken?: string }).idToken = t.idToken;
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

declare module "next-auth" {
  interface Session {
    accessToken?: string;
    error?: "RefreshAccessTokenError";
  }
}
