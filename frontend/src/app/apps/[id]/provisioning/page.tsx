"use client";

// A4 — Per-app provisioning tab.
// Lets admins configure outbound SCIM/Slack/GitHub provisioning for an app,
// view per-user sync state, and trigger manual resyncs.

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { RefreshCw, AlertTriangle, CheckCircle, Clock, XCircle } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getProvisioningConfig,
  upsertProvisioningConfig,
  listProvisioningState,
  resyncUser,
  type ProvisioningConfig,
  type ProvisioningStateEntry,
} from "@/lib/api";
import { useApiReady } from "../../../providers";

const STATUS_ICONS: Record<string, React.ReactNode> = {
  provisioned: <CheckCircle className="h-4 w-4 text-green-500" />,
  pending: <Clock className="h-4 w-4 text-yellow-500" />,
  error: <AlertTriangle className="h-4 w-4 text-red-500" />,
  permanent_error: <XCircle className="h-4 w-4 text-red-700" />,
  deprovisioned: <XCircle className="h-4 w-4 text-gray-400" />,
};

export default function ProvisioningPage() {
  const { id: appId } = useParams<{ id: string }>();
  const apiReady = useApiReady();

  const [config, setConfig] = useState<ProvisioningConfig | null>(null);
  const [state, setState] = useState<ProvisioningStateEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Form state
  const [enabled, setEnabled] = useState(false);
  const [connectorType, setConnectorType] = useState<"scim" | "slack" | "github">("scim");
  const [endpointUrl, setEndpointUrl] = useState("");
  const [bearerToken, setBearerToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveOk, setSaveOk] = useState(false);

  // Resync state — keyed by userId
  const [resyncing, setResyncing] = useState<Record<string, boolean>>({});
  const [resyncMsg, setResyncMsg] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!apiReady || !appId) return;
    const load = async () => {
      try {
        setLoading(true);
        setError(null);
        const [cfg, st] = await Promise.all([
          getProvisioningConfig(appId),
          listProvisioningState(appId),
        ]);
        setConfig(cfg);
        setState(st);
        setEnabled(cfg.enabled);
        setConnectorType(cfg.connectorType);
        setEndpointUrl(cfg.endpointUrl ?? "");
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load provisioning data");
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [apiReady, appId]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!appId) return;
    setSaving(true);
    setSaveError(null);
    setSaveOk(false);
    try {
      const updated = await upsertProvisioningConfig(appId, {
        enabled,
        connectorType,
        endpointUrl: endpointUrl.trim() || undefined,
        bearerToken: bearerToken.trim() || undefined,
      });
      setConfig(updated);
      setBearerToken(""); // never re-echo
      setSaveOk(true);
      setTimeout(() => setSaveOk(false), 3000);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save config");
    } finally {
      setSaving(false);
    }
  };

  const handleResync = async (userId: string) => {
    if (!appId) return;
    setResyncing((prev) => ({ ...prev, [userId]: true }));
    setResyncMsg((prev) => ({ ...prev, [userId]: "" }));
    try {
      await resyncUser(appId, userId);
      setResyncMsg((prev) => ({ ...prev, [userId]: "Queued" }));
    } catch (err: unknown) {
      setResyncMsg((prev) => ({
        ...prev,
        [userId]: err instanceof Error ? err.message : "Failed",
      }));
    } finally {
      setResyncing((prev) => ({ ...prev, [userId]: false }));
    }
  };

  if (loading) {
    return <div className="p-6 text-sm text-gray-500">Loading provisioning config…</div>;
  }

  return (
    <div className="p-6 max-w-3xl space-y-8">
      <div>
        <h1 className="text-xl font-semibold text-gray-900">Outbound Provisioning</h1>
        <p className="mt-1 text-sm text-gray-500">
          Configure automatic account creation in downstream SaaS when users are onboarded.
        </p>
      </div>

      {error && <ErrorBanner message={error} />}

      {/* Config form */}
      <form onSubmit={handleSave} className="space-y-5 bg-white border border-gray-200 rounded-lg p-5">
        <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
          Connector Settings
        </h2>

        {/* Enabled toggle */}
        <div className="flex items-center gap-3">
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            onClick={() => setEnabled((v) => !v)}
            className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 ${
              enabled ? "bg-blue-600" : "bg-gray-200"
            }`}
          >
            <span
              className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
                enabled ? "translate-x-6" : "translate-x-1"
              }`}
            />
          </button>
          <span className="text-sm font-medium text-gray-700">
            {enabled ? "Provisioning enabled" : "Provisioning disabled"}
          </span>
        </div>

        {/* Connector type */}
        <div>
          <label htmlFor="connectorType" className="block text-sm font-medium text-gray-700 mb-1">
            Connector type
          </label>
          <select
            id="connectorType"
            value={connectorType}
            onChange={(e) => setConnectorType(e.target.value as "scim" | "slack" | "github")}
            className="block w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option value="scim">Generic SCIM 2.0</option>
            <option value="slack">Slack</option>
            <option value="github">GitHub Org</option>
          </select>
        </div>

        {/* Endpoint URL — only for generic SCIM */}
        {connectorType === "scim" && (
          <div>
            <label htmlFor="endpointUrl" className="block text-sm font-medium text-gray-700 mb-1">
              SCIM endpoint URL
            </label>
            <input
              id="endpointUrl"
              type="url"
              value={endpointUrl}
              onChange={(e) => setEndpointUrl(e.target.value)}
              placeholder="https://app.example.com/scim/v2"
              className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        )}

        {/* Bearer token — write-only */}
        <div>
          <label htmlFor="bearerToken" className="block text-sm font-medium text-gray-700 mb-1">
            Bearer token
            {config?.bearerTokenConfigured && (
              <span className="ml-2 text-xs text-green-600 font-normal">
                (configured — paste a new value to rotate)
              </span>
            )}
          </label>
          <input
            id="bearerToken"
            type="password"
            autoComplete="new-password"
            value={bearerToken}
            onChange={(e) => setBearerToken(e.target.value)}
            placeholder={config?.bearerTokenConfigured ? "Leave blank to keep existing" : "Enter token"}
            className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {saveError && <p className="text-sm text-red-600">{saveError}</p>}
        {saveOk && <p className="text-sm text-green-600">Saved successfully.</p>}

        <button
          type="submit"
          disabled={saving}
          className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          {saving ? "Saving…" : "Save settings"}
        </button>
      </form>

      {/* Per-user state table */}
      <div>
        <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3">
          Provisioning State
        </h2>
        {state.length === 0 ? (
          <p className="text-sm text-gray-500">
            No provisioning records yet. Users will appear here after onboarding.
          </p>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-gray-200">
            <table className="min-w-full text-sm divide-y divide-gray-200">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    User
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    Status
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    Remote ID
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    Last Sync
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    Retries
                  </th>
                  <th className="px-4 py-3" />
                </tr>
              </thead>
              <tbody className="bg-white divide-y divide-gray-100">
                {state.map((entry) => (
                  <tr key={entry.id} className="hover:bg-gray-50">
                    <td className="px-4 py-3 text-gray-900">
                      {entry.userEmail || entry.userId.slice(0, 8) + "…"}
                    </td>
                    <td className="px-4 py-3">
                      <span className="inline-flex items-center gap-1 capitalize">
                        {STATUS_ICONS[entry.status] ?? null}
                        {entry.status.replace("_", " ")}
                      </span>
                      {entry.lastError && (
                        <p className="text-xs text-red-500 mt-0.5 max-w-xs truncate" title={entry.lastError}>
                          {entry.lastError}
                        </p>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-500 font-mono text-xs">
                      {entry.remoteId || "—"}
                    </td>
                    <td className="px-4 py-3 text-gray-500 text-xs">
                      {entry.lastSyncAt
                        ? new Date(entry.lastSyncAt).toLocaleString()
                        : "—"}
                    </td>
                    <td className="px-4 py-3 text-gray-500 text-center">{entry.retryCount}</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => handleResync(entry.userId)}
                        disabled={resyncing[entry.userId]}
                        className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-blue-600 hover:bg-blue-50 disabled:opacity-50"
                        title="Trigger manual resync"
                      >
                        <RefreshCw
                          className={`h-3 w-3 ${resyncing[entry.userId] ? "animate-spin" : ""}`}
                        />
                        {resyncMsg[entry.userId] || "Resync"}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
