"use client";

import { SessionProvider, useSession } from "next-auth/react";
import { setAuthToken } from "@/lib/api";
import { useEffect } from "react";

function AuthTokenSync({ children }: { children: React.ReactNode }) {
  const { data: session } = useSession();
  useEffect(() => {
    setAuthToken((session as any)?.accessToken || null);
  }, [session]);
  return <>{children}</>;
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SessionProvider>
      <AuthTokenSync>{children}</AuthTokenSync>
    </SessionProvider>
  );
}
