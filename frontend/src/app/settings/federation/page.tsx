"use client";

import { useEffect, useState } from "react";
import { Database, Trash2, RefreshCw, Wifi, WifiOff, Plus, ChevronRight, X } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import ConfirmDialog from "@/components/ConfirmDialog";
import {
  listFederationSources,
  createFederationSource,
  deleteFederationSource,
  testFederationConnection,
  triggerFederationSync,
} from "@/lib/api";
import type { FederationSource, CreateFederationSourceRequest } from "@/lib/api";
import { useApiReady } from "../../providers";

const EMPTY_FORM: CreateFederationSourceRequest = {
  name: "",
  vendor: "other",
  connectionUrl: "",
  bindDn: "",
  usersDn: "",
};

export default function FederationPage() {
  const apiReady = useApiReady();

  const [sources, setSources] = useState<FederationSource[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<CreateFederationSourceRequest>(EMPTY_FORM);
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<FederationSource | null>(null);
  const [deleting, setDeleting] = useState(false);

  const [testingId, setTestingId] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<Record<string, { success: boolean; error?: string }>>({});

  const [syncingId, setSyncingId] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    loadSources();
  }, [apiReady]);

  async function loadSources() {
    setLoading(true);
    setError(null);
    try {
      const data = await listFederationSources();
      setSources(data);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load federation sources");
    } finally {
      setLoading(false);
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setSubmitting(true);
    try {
      const created = await createFederationSource(form);
      setSources((prev) => [created, ...prev]);
      setShowForm(false);
      setForm(EMPTY_FORM);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "Failed to create federation source");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteFederationSource(deleteTarget.id);
      setSources((prev) => prev.filter((s) => s.id !== deleteTarget.id));
      setDeleteTarget(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete federation source");
    } finally {
      setDeleting(false);
    }
  }

  async function handleTest(source: FederationSource) {
    setTestingId(source.id);
    try {
      const result = await testFederationConnection(source.id);
      setTestResults((prev) => ({ ...prev, [source.id]: result }));
    } catch (err) {
      setTestResults((prev) => ({
        ...prev,
        [source.id]: { success: false, error: err instanceof Error ? err.message : "Test failed" },
      }));
    } finally {
      setTestingId(null);
    }
  }

  async function handleSync(source: FederationSource) {
    setSyncingId(source.id);
    try {
      await triggerFederationSync(source.id, "triggerFullSync");
      await loadSources();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Sync failed");
    } finally {
      setSyncingId(null);
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-gray-900">Directory Federation</h1>
          <p className="mt-1 text-sm text-gray-500">
            Connect LDAP or Active Directory sources to sync users automatically.
          </p>
        </div>
        <button
          onClick={() => { setShowForm(true); setFormError(null); setForm(EMPTY_FORM); }}
          className="inline-flex items-center gap-2 rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-indigo-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
        >
          <Plus className="h-4 w-4" />
          Add source
        </button>
      </div>

      {error && <ErrorBanner message={error} onDismiss={() => setError(null)} />}

      {/* Add source form */}
      {showForm && (
        <div className="rounded-lg border border-gray-200 bg-white p-6 shadow-sm">
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-base font-medium text-gray-900">New LDAP/AD Source</h2>
            <button onClick={() => setShowForm(false)} className="text-gray-400 hover:text-gray-600">
              <X className="h-4 w-4" />
            </button>
          </div>
          {formError && <ErrorBanner message={formError} onDismiss={() => setFormError(null)} />}
          <form onSubmit={handleCreate} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700">Display name</label>
              <input
                required
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="Corp AD"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Vendor</label>
              <select
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none"
                value={form.vendor}
                onChange={(e) => setForm({ ...form, vendor: e.target.value })}
              >
                <option value="other">Generic LDAP</option>
                <option value="ad">Active Directory</option>
              </select>
            </div>
            <div className="sm:col-span-2">
              <label className="block text-sm font-medium text-gray-700">Connection URL</label>
              <input
                required
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none"
                value={form.connectionUrl}
                onChange={(e) => setForm({ ...form, connectionUrl: e.target.value })}
                placeholder="ldap://dc.example.com:389"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Bind DN</label>
              <input
                required
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none"
                value={form.bindDn}
                onChange={(e) => setForm({ ...form, bindDn: e.target.value })}
                placeholder="CN=svc-fc,DC=example,DC=com"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700">Users DN</label>
              <input
                required
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none"
                value={form.usersDn}
                onChange={(e) => setForm({ ...form, usersDn: e.target.value })}
                placeholder="OU=Users,DC=example,DC=com"
              />
            </div>
            <div className="sm:col-span-2 flex justify-end gap-3">
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-60"
              >
                {submitting ? "Creating…" : "Create source"}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Source list */}
      {loading ? (
        <p className="text-sm text-gray-500">Loading…</p>
      ) : sources.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border-2 border-dashed border-gray-300 py-12 text-center">
          <Database className="mx-auto h-10 w-10 text-gray-300" />
          <p className="mt-2 text-sm text-gray-500">No federation sources configured.</p>
          <p className="text-xs text-gray-400">Add an LDAP or AD source to start syncing users.</p>
        </div>
      ) : (
        <ul className="divide-y divide-gray-200 rounded-lg border border-gray-200 bg-white shadow-sm">
          {sources.map((source) => {
            const testResult = testResults[source.id];
            return (
              <li key={source.id} className="px-6 py-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex items-start gap-3">
                    <Database className="mt-0.5 h-5 w-5 flex-shrink-0 text-indigo-500" />
                    <div>
                      <p className="font-medium text-gray-900">{source.name}</p>
                      <p className="text-xs text-gray-500">
                        {source.vendor === "ad" ? "Active Directory" : "Generic LDAP"} &middot;{" "}
                        {(source.config?.connectionUrl as string) ?? "—"}
                      </p>
                      {source.lastSyncAt && (
                        <p className="mt-0.5 text-xs text-gray-400">
                          Last sync: {new Date(source.lastSyncAt).toLocaleString()} &middot;{" "}
                          <span className={source.lastSyncStatus === "success" ? "text-green-600" : "text-red-500"}>
                            {source.lastSyncStatus}
                          </span>
                        </p>
                      )}
                      {testResult && (
                        <p className={`mt-1 text-xs ${testResult.success ? "text-green-600" : "text-red-500"}`}>
                          {testResult.success ? "Connection OK" : `Test failed: ${testResult.error}`}
                        </p>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => handleTest(source)}
                      disabled={testingId === source.id}
                      title="Test connection"
                      className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60"
                    >
                      {testingId === source.id ? (
                        <Wifi className="h-3.5 w-3.5 animate-pulse" />
                      ) : testResult?.success === false ? (
                        <WifiOff className="h-3.5 w-3.5 text-red-500" />
                      ) : (
                        <Wifi className="h-3.5 w-3.5" />
                      )}
                      Test
                    </button>
                    <button
                      onClick={() => handleSync(source)}
                      disabled={syncingId === source.id}
                      title="Full sync"
                      className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60"
                    >
                      <RefreshCw className={`h-3.5 w-3.5 ${syncingId === source.id ? "animate-spin" : ""}`} />
                      Sync
                    </button>
                    <button
                      onClick={() => setDeleteTarget(source)}
                      title="Delete source"
                      className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                    <ChevronRight className="h-4 w-4 text-gray-400" />
                  </div>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {/* Delete confirmation */}
      <ConfirmDialog
        isOpen={deleteTarget !== null}
        title="Delete federation source"
        message={`Remove "${deleteTarget?.name}"? This will also remove the associated Keycloak component. Federated users will remain in the local directory but will no longer be synced.`}
        confirmLabel="Delete"
        onConfirm={handleDelete}
        onClose={() => setDeleteTarget(null)}
        variant="danger"
      />
    </div>
  );
}
