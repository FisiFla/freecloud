"use client";

import { SessionProvider, useSession, signOut } from "next-auth/react";
import { setAuthToken } from "@/lib/api";
import { useEffect } from "react";

function AuthTokenSync({ children }: { children: React.ReactNode }) {
  const { data: session } = useSession();
  useEffect(() => {
    setAuthToken(session?.accessToken || null);
  }, [session]);

  // If the access token can no longer be refreshed, force a sign-out so the
  // user is sent back to the sign-in page instead of seeing failing API calls.
  useEffect(() => {
    if (session?.error === "RefreshAccessTokenError") {
      signOut({ callbackUrl: "/signin?error=session_expired" });
    }
  }, [session?.error]);

  return <>{children}</>;
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SessionProvider>
      <AuthTokenSync>{children}</AuthTokenSync>
    </SessionProvider>
  );
}
