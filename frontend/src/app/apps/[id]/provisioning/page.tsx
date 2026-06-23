"use client";

// A4 — Per-app provisioning tab.
// Lets admins configure outbound SCIM/Slack/GitHub provisioning for an app,
// view per-user sync state, and trigger manual resyncs.
// E1: attribute mapping editor + dry-run preview
// E2: reconcile-all button + nextRetryAt column

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { RefreshCw, AlertTriangle, CheckCircle, Clock, XCircle, Plus, Trash2 } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getProvisioningConfig,
  upsertProvisioningConfig,
  listProvisioningState,
  resyncUser,
  dryRunProvisioning,
  reconcileAll,
  type ProvisioningConfig,
  type ProvisioningStateEntry,
  type DryRunProvisioningResponse,
} from "@/lib/api";
import { useApiReady } from "../../../providers";

const STATUS_ICONS: Record<string, React.ReactNode> = {
  provisioned: <CheckCircle className="h-4 w-4 text-green-500" />,
  pending: <Clock className="h-4 w-4 text-yellow-500" />,
  error: <AlertTriangle className="h-4 w-4 text-red-500" />,
  permanent_error: <XCircle className="h-4 w-4 text-red-700" />,
  deprovisioned: <XCircle className="h-4 w-4 text-gray-400" />,
};

const DEFAULT_SCIM_FIELDS = ["userName", "givenName", "familyName", "department"];

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

  // E1: Attribute mapping editor state — list of [freecloudField, remoteAttribute] pairs
  const [attrMapRows, setAttrMapRows] = useState<{ field: string; remote: string }[]>([]);

  // Resync state — keyed by userId
  const [resyncing, setResyncing] = useState<Record<string, boolean>>({});
  const [resyncMsg, setResyncMsg] = useState<Record<string, string>>({});

  // E2: Reconcile-all state
  const [reconciling, setReconciling] = useState(false);
  const [reconcileMsg, setReconcileMsg] = useState<string | null>(null);

  // E1: Dry-run state
  const [dryRunUserId, setDryRunUserId] = useState("");
  const [dryRunning, setDryRunning] = useState(false);
  const [dryRunResult, setDryRunResult] = useState<DryRunProvisioningResponse | null>(null);
  const [dryRunError, setDryRunError] = useState<string | null>(null);

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
        // Populate attribute map rows from config
        const rows = Object.entries(cfg.attributeMap ?? {}).map(([field, remote]) => ({
          field,
          remote,
        }));
        setAttrMapRows(rows);
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
      // Build attributeMap from rows, filtering empty entries
      const attributeMap: Record<string, string> = {};
      for (const row of attrMapRows) {
        if (row.field.trim() && row.remote.trim()) {
          attributeMap[row.field.trim()] = row.remote.trim();
        }
      }
      const updated = await upsertProvisioningConfig(appId, {
        enabled,
        connectorType,
        endpointUrl: endpointUrl.trim() || undefined,
        bearerToken: bearerToken.trim() || undefined,
        attributeMap: Object.keys(attributeMap).length > 0 ? attributeMap : undefined,
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

  const handleReconcileAll = async () => {
    if (!appId) return;
    setReconciling(true);
    setReconcileMsg(null);
    try {
      await reconcileAll(appId);
      setReconcileMsg("Reconciliation queued.");
    } catch (err: unknown) {
      setReconcileMsg(err instanceof Error ? err.message : "Failed to queue reconciliation");
    } finally {
      setReconciling(false);
    }
  };

  const handleDryRun = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!appId || !dryRunUserId.trim()) return;
    setDryRunning(true);
    setDryRunResult(null);
    setDryRunError(null);
    try {
      const result = await dryRunProvisioning(appId, dryRunUserId.trim());
      setDryRunResult(result);
    } catch (err: unknown) {
      setDryRunError(err instanceof Error ? err.message : "Dry run failed");
    } finally {
      setDryRunning(false);
    }
  };

  const addAttrRow = () => {
    setAttrMapRows((prev) => [...prev, { field: "", remote: "" }]);
  };

  const removeAttrRow = (idx: number) => {
    setAttrMapRows((prev) => prev.filter((_, i) => i !== idx));
  };

  const updateAttrRow = (idx: number, key: "field" | "remote", value: string) => {
    setAttrMapRows((prev) =>
      prev.map((row, i) => (i === idx ? { ...row, [key]: value } : row)),
    );
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

        {/* E1: Attribute mapping editor */}
        <div>
          <div className="flex items-center justify-between mb-2">
            <label className="block text-sm font-medium text-gray-700">
              Attribute mapping
            </label>
            <button
              type="button"
              onClick={addAttrRow}
              className="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700"
            >
              <Plus className="h-3 w-3" />
              Add mapping
            </button>
          </div>
          <p className="text-xs text-gray-500 mb-2">
            Map FreeCloud field names to remote attribute names. Default fields:{" "}
            {DEFAULT_SCIM_FIELDS.join(", ")}.
          </p>
          {attrMapRows.length === 0 ? (
            <p className="text-xs text-gray-400 italic">No custom mappings — defaults will be used.</p>
          ) : (
            <div className="space-y-2">
              {attrMapRows.map((row, idx) => (
                <div key={idx} className="flex items-center gap-2">
                  <input
                    type="text"
                    value={row.field}
                    onChange={(e) => updateAttrRow(idx, "field", e.target.value)}
                    placeholder="FreeCloud field (e.g. userName)"
                    className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm focus:border-blue-500 focus:outline-none"
                  />
                  <span className="text-gray-400 text-sm">→</span>
                  <input
                    type="text"
                    value={row.remote}
                    onChange={(e) => updateAttrRow(idx, "remote", e.target.value)}
                    placeholder="Remote attribute name"
                    className="flex-1 rounded border border-gray-300 px-2 py-1 text-sm focus:border-blue-500 focus:outline-none"
                  />
                  <button
                    type="button"
                    onClick={() => removeAttrRow(idx)}
                    className="text-gray-400 hover:text-red-500"
                    title="Remove mapping"
                  >
                    <Trash2 className="h-4 w-4" />
                  </button>
                </div>
              ))}
            </div>
          )}
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

      {/* E1: Dry-run preview */}
      <div className="bg-white border border-gray-200 rounded-lg p-5 space-y-4">
        <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
          Dry-Run Preview
        </h2>
        <p className="text-sm text-gray-500">
          Preview what payload would be sent for a user without calling the connector.
        </p>
        <form onSubmit={handleDryRun} className="flex items-end gap-3">
          <div className="flex-1">
            <label htmlFor="dryRunUserId" className="block text-sm font-medium text-gray-700 mb-1">
              User ID (UUID)
            </label>
            <input
              id="dryRunUserId"
              type="text"
              value={dryRunUserId}
              onChange={(e) => setDryRunUserId(e.target.value)}
              placeholder="00000000-0000-0000-0000-000000000000"
              className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500 font-mono"
            />
          </div>
          <button
            type="submit"
            disabled={dryRunning || !dryRunUserId.trim()}
            className="inline-flex items-center gap-2 rounded-md bg-gray-800 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-gray-900 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-gray-500"
          >
            {dryRunning ? "Previewing…" : "Preview"}
          </button>
        </form>
        {dryRunError && <p className="text-sm text-red-600">{dryRunError}</p>}
        {dryRunResult && (
          <div className="rounded-md bg-gray-50 border border-gray-200 p-3">
            <p className="text-xs font-medium text-gray-500 mb-1">
              Connector: {dryRunResult.connectorType}
              {dryRunResult.endpointUrl && ` — ${dryRunResult.endpointUrl}`}
            </p>
            <pre className="text-xs text-gray-800 overflow-auto whitespace-pre-wrap break-all">
              {JSON.stringify(dryRunResult.payload, null, 2)}
            </pre>
          </div>
        )}
      </div>

      {/* E2: Provisioning State table */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
            Provisioning State
          </h2>
          <div className="flex items-center gap-3">
            {reconcileMsg && (
              <span className="text-xs text-gray-600">{reconcileMsg}</span>
            )}
            <button
              onClick={handleReconcileAll}
              disabled={reconciling}
              className="inline-flex items-center gap-1 rounded-md border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
              title="Reset all error/pending records and trigger reconciliation"
            >
              <RefreshCw className={`h-3 w-3 ${reconciling ? "animate-spin" : ""}`} />
              Reconcile All
            </button>
          </div>
        </div>
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
                  <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wide">
                    Next Retry
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
                    <td className="px-4 py-3 text-gray-500 text-xs">
                      {entry.nextRetryAt
                        ? new Date(entry.nextRetryAt).toLocaleString()
                        : "—"}
                    </td>
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
