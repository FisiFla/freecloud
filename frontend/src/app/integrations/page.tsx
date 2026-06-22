"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Database, Plug2, Key, ChevronRight, Loader2 } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  listFederationSources,
  listApps,
  getProvisioningConfig,
  listAPITokens,
} from "@/lib/api";
import type { FederationSource, App, ProvisioningConfig, APIToken } from "@/lib/api";
import { useApiReady } from "../providers";

interface ProvisionedApp {
  app: App;
  config: ProvisioningConfig;
}

export default function IntegrationsPage() {
  const apiReady = useApiReady();

  // Federation
  const [federationSources, setFederationSources] = useState<FederationSource[]>([]);
  const [federationLoading, setFederationLoading] = useState(true);
  const [federationError, setFederationError] = useState<string | null>(null);

  // Outbound provisioning
  const [provisionedApps, setProvisionedApps] = useState<ProvisionedApp[]>([]);
  const [provisioningLoading, setProvisioningLoading] = useState(true);
  const [provisioningError, setProvisioningError] = useState<string | null>(null);

  // Inbound SCIM tokens
  const [apiTokens, setApiTokens] = useState<APIToken[]>([]);
  const [tokensLoading, setTokensLoading] = useState(true);
  const [tokensError, setTokensError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;

    // Load federation sources
    (async () => {
      setFederationLoading(true);
      setFederationError(null);
      try {
        const data = await listFederationSources();
        setFederationSources(data);
      } catch (e) {
        setFederationError(e instanceof Error ? e.message : "Failed to load federation sources");
      } finally {
        setFederationLoading(false);
      }
    })();

    // Load provisioned apps
    (async () => {
      setProvisioningLoading(true);
      setProvisioningError(null);
      try {
        const apps = await listApps();
        const results = await Promise.allSettled(
          apps.map((app) => getProvisioningConfig(app.id).then((cfg) => ({ app, config: cfg }))),
        );
        const enabled: ProvisionedApp[] = [];
        for (const result of results) {
          if (result.status === "fulfilled" && result.value.config.enabled) {
            enabled.push(result.value);
          }
        }
        setProvisionedApps(enabled);
      } catch (e) {
        setProvisioningError(e instanceof Error ? e.message : "Failed to load provisioning data");
      } finally {
        setProvisioningLoading(false);
      }
    })();

    // Load API tokens
    (async () => {
      setTokensLoading(true);
      setTokensError(null);
      try {
        const data = await listAPITokens();
        setApiTokens(data);
      } catch (e) {
        setTokensError(e instanceof Error ? e.message : "Failed to load API tokens");
      } finally {
        setTokensLoading(false);
      }
    })();
  }, [apiReady]);

  function syncStatusBadge(status?: string) {
    if (!status) {
      return (
        <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-500">
          Never synced
        </span>
      );
    }
    if (status === "success") {
      return (
        <span className="inline-flex items-center rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700">
          {status}
        </span>
      );
    }
    return (
      <span className="inline-flex items-center rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-700">
        {status}
      </span>
    );
  }

  function connectorBadge(connectorType: string) {
    return (
      <span className="inline-flex items-center rounded-full bg-indigo-100 px-2 py-0.5 text-xs font-medium text-indigo-700 uppercase">
        {connectorType}
      </span>
    );
  }

  return (
    <div className="space-y-8">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-semibold text-gray-900">Integrations</h1>
        <p className="mt-1 text-sm text-gray-500">
          Live status of all directory, provisioning, and SCIM integrations.
        </p>
      </div>

      {/* ── Section 1: Directory Federation ─────────────────────────────── */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
            Directory Federation
          </h2>
          <Link
            href="/settings/federation"
            className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-800"
          >
            Configure <ChevronRight className="h-3.5 w-3.5" />
          </Link>
        </div>

        {federationError && (
          <ErrorBanner message={federationError} onDismiss={() => setFederationError(null)} />
        )}

        <div className="rounded-lg border border-gray-200 bg-white shadow-sm">
          {federationLoading ? (
            <div className="flex items-center gap-2 p-5 text-sm text-gray-500">
              <Loader2 className="h-4 w-4 animate-spin" />
              Loading…
            </div>
          ) : federationSources.length === 0 ? (
            <p className="p-5 text-sm text-gray-500 italic">No federation sources configured.</p>
          ) : (
            <ul className="divide-y divide-gray-100">
              {federationSources.map((source) => (
                <li key={source.id} className="flex items-center justify-between gap-4 px-5 py-4">
                  <div className="flex items-start gap-3">
                    <Database className="mt-0.5 h-5 w-5 flex-shrink-0 text-indigo-400" />
                    <div>
                      <p className="text-sm font-medium text-gray-900">{source.name}</p>
                      <p className="text-xs text-gray-500">
                        {source.vendor === "ad" ? "Active Directory" : "Generic LDAP"}
                        {source.lastSyncAt && (
                          <> &middot; Last sync: {new Date(source.lastSyncAt).toLocaleString()}</>
                        )}
                      </p>
                    </div>
                  </div>
                  {syncStatusBadge(source.lastSyncStatus)}
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      {/* ── Section 2: Outbound Provisioning ─────────────────────────────── */}
      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
          Outbound Provisioning
        </h2>

        {provisioningError && (
          <ErrorBanner message={provisioningError} onDismiss={() => setProvisioningError(null)} />
        )}

        <div className="rounded-lg border border-gray-200 bg-white shadow-sm">
          {provisioningLoading ? (
            <div className="flex items-center gap-2 p-5 text-sm text-gray-500">
              <Loader2 className="h-4 w-4 animate-spin" />
              Loading…
            </div>
          ) : provisionedApps.length === 0 ? (
            <p className="p-5 text-sm text-gray-500 italic">
              No apps have outbound provisioning enabled.
            </p>
          ) : (
            <ul className="divide-y divide-gray-100">
              {provisionedApps.map(({ app, config }) => (
                <li key={app.id} className="flex items-center justify-between gap-4 px-5 py-4">
                  <div className="flex items-start gap-3">
                    <Plug2 className="mt-0.5 h-5 w-5 flex-shrink-0 text-indigo-400" />
                    <div>
                      <p className="text-sm font-medium text-gray-900">{app.name}</p>
                      <p className="text-xs text-gray-500">{app.protocol}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    {connectorBadge(config.connectorType)}
                    <Link
                      href={`/apps/${app.id}/provisioning`}
                      className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-800"
                    >
                      Configure <ChevronRight className="h-3.5 w-3.5" />
                    </Link>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      {/* ── Section 3: Inbound SCIM Clients ──────────────────────────────── */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
            Inbound SCIM Clients
          </h2>
          <Link
            href="/settings/api-tokens"
            className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-800"
          >
            Manage <ChevronRight className="h-3.5 w-3.5" />
          </Link>
        </div>

        {tokensError && (
          <ErrorBanner message={tokensError} onDismiss={() => setTokensError(null)} />
        )}

        <div className="rounded-lg border border-gray-200 bg-white shadow-sm">
          {tokensLoading ? (
            <div className="flex items-center gap-2 p-5 text-sm text-gray-500">
              <Loader2 className="h-4 w-4 animate-spin" />
              Loading…
            </div>
          ) : apiTokens.length === 0 ? (
            <p className="p-5 text-sm text-gray-500 italic">
              No API tokens configured. Create a token in Settings → API Tokens to allow SCIM clients
              to provision users.
            </p>
          ) : (
            <ul className="divide-y divide-gray-100">
              {apiTokens.map((token) => (
                <li key={token.id} className="flex items-center justify-between gap-4 px-5 py-4">
                  <div className="flex items-start gap-3">
                    <Key className="mt-0.5 h-5 w-5 flex-shrink-0 text-indigo-400" />
                    <div>
                      <p className="text-sm font-medium text-gray-900">{token.name}</p>
                      <p className="text-xs text-gray-500">
                        {token.serviceIdentity && <>{token.serviceIdentity} &middot; </>}
                        Role: {token.role}
                        {token.expiresAt && (
                          <> &middot; Expires: {new Date(token.expiresAt).toLocaleDateString()}</>
                        )}
                      </p>
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </div>
  );
}
