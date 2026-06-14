"use client";

import { SessionProvider, useSession, signOut } from "next-auth/react";
import { setAuthToken } from "@/lib/api";
import { createContext, useContext, useEffect, useState } from "react";

// ApiReadyContext exposes whether the access token has been published to the
// API client, so consumers can synchronously gate fetches without polling.
const ApiReadyContext = createContext(false);

/**
 * useApiReady returns true once the session's access token has been synced
 * into the API client. Use this to avoid firing authenticated requests before
 * the token is available.
 */
export function useApiReady(): boolean {
  return useContext(ApiReadyContext);
}

function AuthTokenSync({ children }: { children: React.ReactNode }) {
  const { data: session } = useSession();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const token = session?.accessToken || null;
    setAuthToken(token);
    setReady(!!token);
  }, [session]);

  // If the access token can no longer be refreshed, force a sign-out so the
  // user is sent back to the sign-in page instead of seeing failing API calls.
  useEffect(() => {
    if (session?.error === "RefreshAccessTokenError") {
      signOut({ callbackUrl: "/signin?error=session_expired" });
    }
  }, [session?.error]);

  return <ApiReadyContext.Provider value={ready}>{children}</ApiReadyContext.Provider>;
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SessionProvider>
      <AuthTokenSync>{children}</AuthTokenSync>
    </SessionProvider>
  );
}
