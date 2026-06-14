import NextAuth from "next-auth";
import KeycloakProvider from "next-auth/providers/keycloak";

// Mark this route as dynamic — it must never be statically prerendered.
export const dynamic = "force-dynamic";

const handler = NextAuth({
  providers: [
    KeycloakProvider({
      clientId: process.env.KEYCLOAK_CLIENT_ID || "freecloud-dashboard",
      clientSecret: process.env.KEYCLOAK_CLIENT_SECRET || "",
      issuer: process.env.KEYCLOAK_ISSUER || "http://localhost:8081/realms/freecloud",
    }),
  ],
  callbacks: {
    async jwt({ token, account }) {
      // Initial sign-in
      if (account) {
        token.accessToken = account.access_token;
        token.idToken = account.id_token;
        token.refreshToken = account.refresh_token;
        token.expiresAt = Math.floor(Date.now() / 1000) + (Number(account.expires_in) || 0);
        return token;
      }

      // If token is still valid for more than 30 seconds, return it
      if (token.expiresAt && Date.now() / 1000 < (token.expiresAt as number) - 30) {
        return token;
      }

      // Token is about to expire — try refreshing
      try {
        const issuer = process.env.KEYCLOAK_ISSUER || "http://localhost:8081/realms/freecloud";
        const res = await fetch(`${issuer}/protocol/openid-connect/token`, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            client_id: process.env.KEYCLOAK_CLIENT_ID || "freecloud-dashboard",
            client_secret: process.env.KEYCLOAK_CLIENT_SECRET || "",
            grant_type: "refresh_token",
            refresh_token: token.refreshToken as string,
          }),
        });

        if (!res.ok) {
          token.error = "RefreshAccessTokenError";
          return token;
        }

        const tokens = await res.json();
        token.accessToken = tokens.access_token;
        token.idToken = tokens.id_token || token.idToken;
        token.refreshToken = tokens.refresh_token || token.refreshToken;
        token.expiresAt = Math.floor(Date.now() / 1000) + (Number(tokens.expires_in) || 0);
      } catch {
        token.error = "RefreshAccessTokenError";
      }

      return token;
    },
    async session({ session, token }) {
      (session as any).accessToken = token.accessToken;
      (session as any).idToken = token.idToken;
      (session as any).error = token.error;
      return session;
    },
    async signIn({ account, profile }) {
      if (account?.provider === "keycloak") {
        return profile?.sub ? true : false;
      }
      return true;
    },
    async redirect({ url, baseUrl }) {
      // Returns the full URL for Keycloak redirects
      if (url.startsWith("/")) return `${baseUrl}${url}`;
      if (new URL(url).origin === baseUrl) return url;
      return baseUrl;
    },
  },
  pages: {
    signIn: "/signin",
    error: "/signin?error=auth_failed",
  },
});

export { handler as GET, handler as POST };
