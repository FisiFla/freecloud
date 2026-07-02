"use client";

import { SessionProvider, useSession, signOut } from "next-auth/react";
import { setAuthToken, setActiveOrgId, getMe } from "@/lib/api";
import type { MeResponse } from "@/lib/api";
import { createContext, useCallback, useContext, useEffect, useState } from "react";

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

// ---- Org context (Epic C multi-tenant) ----
//
// ORG_STORAGE_KEY persists the user's last-selected organization across
// reloads. The active org is threaded to every API call via
// setActiveOrgId (see lib/api.ts's X-Org-Id header injection).
const ORG_STORAGE_KEY = "fc-active-org-id";

interface OrgContextValue {
  me: MeResponse | null;
  loading: boolean;
  error: string | null;
  activeOrgId: string;
  setActiveOrg: (orgId: string) => void;
  refresh: () => Promise<void>;
}

const OrgContext = createContext<OrgContextValue>({
  me: null,
  loading: true,
  error: null,
  activeOrgId: "",
  setActiveOrg: () => {},
  refresh: async () => {},
});

/**
 * useOrg exposes the caller's identity, org memberships, and the currently
 * active organization (already synced to the API client's X-Org-Id header).
 * Use this to build an org switcher or gate org-admin-only UI.
 */
export function useOrg(): OrgContextValue {
  return useContext(OrgContext);
}

function OrgSync({ children }: { children: React.ReactNode }) {
  const apiReady = useApiReady();
  const [me, setMe] = useState<MeResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeOrgId, setActiveOrgIdState] = useState("");

  const applyOrg = useCallback((orgId: string) => {
    setActiveOrgIdState(orgId);
    setActiveOrgId(orgId || null);
    if (typeof window !== "undefined") {
      if (orgId) window.localStorage.setItem(ORG_STORAGE_KEY, orgId);
      else window.localStorage.removeItem(ORG_STORAGE_KEY);
    }
  }, []);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const result = await getMe();
      setMe(result);
      // Prefer a previously-selected org if the caller still belongs to it;
      // otherwise fall back to whatever the backend resolved as active.
      const stored = typeof window !== "undefined" ? window.localStorage.getItem(ORG_STORAGE_KEY) : null;
      const stillMember = stored && result.orgs.some((o) => o.orgId === stored);
      applyOrg(stillMember ? stored! : result.activeOrgId);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load organization context");
    } finally {
      setLoading(false);
    }
  }, [applyOrg]);

  useEffect(() => {
    if (!apiReady) return;
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const setActiveOrg = useCallback(
    (orgId: string) => {
      applyOrg(orgId);
      // Re-fetch /me so activeRole reflects the newly-selected org.
      refresh();
    },
    [applyOrg, refresh],
  );

  return (
    <OrgContext.Provider value={{ me, loading, error, activeOrgId, setActiveOrg, refresh }}>
      {children}
    </OrgContext.Provider>
  );
}

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SessionProvider>
      <AuthTokenSync>
        <OrgSync>{children}</OrgSync>
      </AuthTokenSync>
    </SessionProvider>
  );
}
