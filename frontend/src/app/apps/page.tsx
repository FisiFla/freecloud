"use client";

import { useEffect, useState } from "react";
import { Plus, Globe, AlertCircle, UserPlus } from "lucide-react";
import SlideOver from "@/components/SlideOver";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import { listApps, createApp, listUsers, assignAppToUser, getAppPolicy, upsertAppPolicy } from "@/lib/api";
import type { App, User, CreateAppResponse, AppAccessPolicy } from "@/lib/api";
import { useApiReady } from "../providers";

export default function AppsPage() {
  const apiReady = useApiReady();
  const [apps, setApps] = useState<App[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [newName, setNewName] = useState("");
  const [newProtocol, setNewProtocol] = useState<"OIDC" | "SAML">("OIDC");
  const [newRedirectUris, setNewRedirectUris] = useState("");
  const [newBaseUrl, setNewBaseUrl] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [samlMetadata, setSamlMetadata] = useState<{ entityId: string; acsUrl: string } | null>(null);

  // Policy dialog state
  const [policyAppId, setPolicyAppId] = useState<string | null>(null);
  const [policyAppName, setPolicyAppName] = useState<string>("");
  const [policy, setPolicy] = useState<AppAccessPolicy | null>(null);
  const [policyLoading, setPolicyLoading] = useState(false);
  const [policySaving, setPolicySaving] = useState(false);
  const [policyError, setPolicyError] = useState<string | null>(null);

  // Assign dialog state
  const [assignAppId, setAssignAppId] = useState<string | null>(null);
  const [assignUserId, setAssignUserId] = useState("");
  const [assigning, setAssigning] = useState(false);
  const [assignMessage, setAssignMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchApps = async () => {
      try {
        setLoading(true);
        setError(null);
        const data = await listApps();
        setApps(data);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load apps");
      } finally {
        setLoading(false);
      }
    };
    fetchApps();
  }, [apiReady]);

  useEffect(() => {
    if (!apiReady) return;
    const fetchUsers = async () => {
      try {
        const data = await listUsers();
        setUsers(Array.isArray(data) ? data : []);
      } catch {
        // Silently ignore — users are optional for the assign flow
      }
    };
    fetchUsers();
  }, [apiReady]);

  const handleAddApp = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setSubmitError(null);
    setSamlMetadata(null);
    try {
      const created = await createApp({
        name: newName,
        protocol: newProtocol,
        redirectURIs: newRedirectUris.split("\n").map((s) => s.trim()).filter(Boolean),
        baseURL: newBaseUrl,
      }) as CreateAppResponse & { enabled?: boolean; createdAt?: string };
      // Add to list (cast to App shape for display)
      setApps((prev) => [...prev, {
        id: created.id,
        name: created.name,
        keycloakClientId: created.keycloakClientId,
        protocol: newProtocol,
        baseUrl: newBaseUrl,
        enabled: true,
      }]);
      // Surface SAML SP metadata so the admin can configure the SP
      if (newProtocol === "SAML" && created.samlEntityId) {
        setSamlMetadata({ entityId: created.samlEntityId, acsUrl: created.samlAcsUrl ?? "" });
      } else {
        setShowAdd(false);
        setNewName("");
        setNewProtocol("OIDC");
        setNewRedirectUris("");
        setNewBaseUrl("");
      }
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create app");
    } finally {
      setSubmitting(false);
    }
  };

  const openPolicy = async (app: App) => {
    setPolicyAppId(app.id);
    setPolicyAppName(app.name);
    setPolicyError(null);
    setPolicyLoading(true);
    try {
      const p = await getAppPolicy(app.id);
      setPolicy(p);
    } catch (err: unknown) {
      setPolicyError(err instanceof Error ? err.message : "Failed to load policy");
      setPolicy({ appId: app.id, requireEnrolled: false, requireDiskEncrypted: false, requireNoCriticalVulns: false });
    } finally {
      setPolicyLoading(false);
    }
  };

  const savePolicy = async () => {
    if (!policy || !policyAppId) return;
    setPolicySaving(true);
    setPolicyError(null);
    try {
      const updated = await upsertAppPolicy(policyAppId, {
        requireEnrolled: policy.requireEnrolled,
        requireDiskEncrypted: policy.requireDiskEncrypted,
        requireNoCriticalVulns: policy.requireNoCriticalVulns,
        maxOsAgeDays: policy.maxOsAgeDays,
      });
      setPolicy(updated);
      setPolicyAppId(null);
    } catch (err: unknown) {
      setPolicyError(err instanceof Error ? err.message : "Failed to save policy");
    } finally {
      setPolicySaving(false);
    }
  };

  const handleAssign = async () => {
    if (!assignAppId || !assignUserId) return;
    setAssigning(true);
    setAssignMessage(null);
    try {
      await assignAppToUser(assignAppId, assignUserId);
      setAssignMessage({ type: "success", text: "Application assigned successfully!" });
    } catch (err: unknown) {
      setAssignMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to assign app" });
    } finally {
      setAssigning(false);
    }
  };

  return (
    <>
      <div>
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">App Catalog</h1>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Manage SSO-connected applications.</p>
          </div>
          <button
            onClick={() => setShowAdd(true)}
            className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            <Plus className="h-4 w-4" />
            Add Application
          </button>
        </div>

        {/* Error banner */}
        {error && (
          <div className="mt-4">
            <ErrorBanner message={error} onDismiss={() => setError(null)} />
          </div>
        )}

        {/* Loading skeleton */}
        {loading ? (
          <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-40 animate-pulse rounded-xl bg-slate-200 dark:bg-slate-700" />
            ))}
          </div>
        ) : (
          /* Grid */
          <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {apps.map((app) => (
              <div
                key={app.id}
                className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition-shadow hover:shadow-md dark:border-slate-700 dark:bg-slate-900"
              >
                <div className="flex items-start justify-between">
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600 dark:bg-indigo-950 dark:text-indigo-400">
                    <Globe className="h-5 w-5" />
                  </div>
                </div>

                <h3 className="mt-4 font-semibold text-slate-800 dark:text-slate-100">{app.name}</h3>
                <p className="mt-1 text-xs text-slate-500 truncate dark:text-slate-400">{app.baseUrl}</p>

                <div className="mt-4 flex items-center gap-2">
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      app.protocol === "OIDC"
                        ? "bg-sky-50 text-sky-700"
                        : "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
                    }`}
                  >
                    {app.protocol}
                  </span>
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      app.enabled
                        ? "bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                        : "bg-slate-100 text-slate-500 dark:bg-slate-700 dark:text-slate-400"
                    }`}
                  >
                    {app.enabled ? "Enabled" : "Disabled"}
                  </span>
                </div>

                {/* Action buttons */}
                <div className="mt-4 flex gap-2">
                  <button
                    onClick={() => { setAssignAppId(app.id); setAssignUserId(""); setAssignMessage(null); }}
                    className="flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-slate-200 px-3 py-2 text-xs font-medium text-slate-600 transition-colors hover:bg-indigo-50 hover:text-indigo-700 hover:border-indigo-200 dark:border-slate-700 dark:text-slate-400"
                  >
                    <UserPlus className="h-3.5 w-3.5" />
                    Assign
                  </button>
                  <button
                    onClick={() => openPolicy(app)}
                    className="flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-slate-200 px-3 py-2 text-xs font-medium text-slate-600 transition-colors hover:bg-amber-50 hover:text-amber-700 hover:border-amber-200 dark:border-slate-700 dark:text-slate-400"
                  >
                    Access Policy
                  </button>
                </div>
              </div>
            ))}
            {apps.length === 0 && (
              <div className="col-span-full">
                <EmptyState
                  icon={Globe}
                  title="No applications configured yet"
                  description="Add your first SSO application to get started."
                />
              </div>
            )}
          </div>
        )}
      </div>

      {/* Add App Slide Over */}
      <SlideOver isOpen={showAdd} onClose={() => { setShowAdd(false); setSamlMetadata(null); setNewName(""); setNewProtocol("OIDC"); setNewRedirectUris(""); setNewBaseUrl(""); }} title="Add Application">
        <form onSubmit={handleAddApp} className="space-y-5">
          {submitError && (
            <div className="flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
              <AlertCircle className="h-5 w-5 shrink-0" />
              <span>{submitError}</span>
            </div>
          )}
          <div>
            <label htmlFor="app-name" className="block text-sm font-medium text-slate-700 dark:text-slate-300">Application Name</label>
            <input
              id="app-name"
              type="text"
              required
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
              placeholder="My App"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">Protocol</label>
            <div className="mt-2 flex gap-4">
              <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
                <input
                  type="radio"
                  name="protocol"
                  value="OIDC"
                  checked={newProtocol === "OIDC"}
                  onChange={() => setNewProtocol("OIDC")}
                  className="text-indigo-600 focus:ring-indigo-500"
                />
                OIDC
              </label>
              <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
                <input
                  type="radio"
                  name="protocol"
                  value="SAML"
                  checked={newProtocol === "SAML"}
                  onChange={() => setNewProtocol("SAML")}
                  className="text-indigo-600 focus:ring-indigo-500"
                />
                SAML
              </label>
            </div>
          </div>

          <div>
            <label htmlFor="app-redirect-uris" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
              Redirect URIs <span className="text-slate-400">(one per line)</span>
            </label>
            <textarea
              id="app-redirect-uris"
              rows={4}
              value={newRedirectUris}
              onChange={(e) => setNewRedirectUris(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
              placeholder="https://myapp.com/callback"
            />
            {newProtocol === "OIDC" && !newRedirectUris.trim() && (
              <p className="mt-1 text-xs text-amber-600">At least one redirect URI is required for OIDC apps</p>
            )}
            {newRedirectUris.trim() && (
              <p className="mt-1 text-xs text-slate-400 dark:text-slate-500">Redirect URIs must start with https:// or http://localhost</p>
            )}
          </div>

          <div>
            <label htmlFor="app-base-url" className="block text-sm font-medium text-slate-700 dark:text-slate-300">Base URL</label>
            <input
              id="app-base-url"
              type="url"
              required
              value={newBaseUrl}
              onChange={(e) => setNewBaseUrl(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
              placeholder="https://myapp.com"
            />
          </div>

          {newProtocol === "SAML" && (
            <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
              <p className="font-medium">SAML SP Metadata</p>
              <p className="mt-1">After creation, copy the <strong>Entity ID</strong> and <strong>ACS URL</strong> into your Service Provider configuration. The Entity ID defaults to the Base URL; the ACS URL is the first Redirect URI you enter above.</p>
            </div>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            {submitting ? "Creating..." : "Create Application"}
          </button>
        </form>

        {/* SAML SP metadata panel shown after successful creation */}
        {samlMetadata && (
          <div className="mt-6 rounded-lg border border-emerald-200 bg-emerald-50 p-4 space-y-3 dark:border-emerald-800 dark:bg-emerald-950">
            <p className="text-sm font-semibold text-emerald-800 dark:text-emerald-300">Application created — copy these into your SP configuration:</p>
            <div>
              <p className="text-xs font-medium text-emerald-700 dark:text-emerald-400">SP Entity ID</p>
              <code className="block mt-1 rounded bg-white px-2 py-1 text-xs text-slate-700 border border-emerald-200 break-all dark:bg-slate-800 dark:text-slate-300 dark:border-emerald-800">{samlMetadata.entityId}</code>
            </div>
            <div>
              <p className="text-xs font-medium text-emerald-700 dark:text-emerald-400">ACS URL (POST binding)</p>
              <code className="block mt-1 rounded bg-white px-2 py-1 text-xs text-slate-700 border border-emerald-200 break-all dark:bg-slate-800 dark:text-slate-300 dark:border-emerald-800">{samlMetadata.acsUrl || "—"}</code>
            </div>
            <button
              onClick={() => { setSamlMetadata(null); setShowAdd(false); setNewName(""); setNewProtocol("OIDC"); setNewRedirectUris(""); setNewBaseUrl(""); }}
              className="w-full rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700"
            >
              Done
            </button>
          </div>
        )}
      </SlideOver>

      {/* Access Policy dialog modal */}
      {policyAppId && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="fixed inset-0 bg-black/40" onClick={() => setPolicyAppId(null)} />
          <div className="relative w-full max-w-sm rounded-xl bg-white p-6 shadow-2xl dark:bg-slate-900">
            <h3 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Access Policy</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">{policyAppName}</p>

            {policyLoading ? (
              <div className="mt-4 space-y-3">
                {[1, 2, 3].map((i) => <div key={i} className="h-6 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />)}
              </div>
            ) : (
              <div className="mt-4 space-y-3">
                {policyError && (
                  <p className="text-sm text-red-600 dark:text-red-400">{policyError}</p>
                )}
                {policy && (
                  <>
                    <label className="flex items-center gap-3 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={policy.requireEnrolled}
                        onChange={(e) => setPolicy({ ...policy, requireEnrolled: e.target.checked })}
                        className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                      />
                      <span className="text-sm text-slate-700 dark:text-slate-300">Require device enrolled in MDM</span>
                    </label>
                    <label className="flex items-center gap-3 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={policy.requireDiskEncrypted}
                        onChange={(e) => setPolicy({ ...policy, requireDiskEncrypted: e.target.checked })}
                        className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                      />
                      <span className="text-sm text-slate-700 dark:text-slate-300">Require disk encryption</span>
                    </label>
                    <label className="flex items-center gap-3 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={policy.requireNoCriticalVulns}
                        onChange={(e) => setPolicy({ ...policy, requireNoCriticalVulns: e.target.checked })}
                        className="h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500"
                      />
                      <span className="text-sm text-slate-700 dark:text-slate-300">Require no critical vulnerabilities</span>
                    </label>
                    <div>
                      <label htmlFor="policy-max-os-age" className="block text-sm font-medium text-slate-700 dark:text-slate-300">Max OS age (days)</label>
                      <input
                        id="policy-max-os-age"
                        type="number"
                        min={0}
                        value={policy.maxOsAgeDays ?? ""}
                        onChange={(e) => setPolicy({
                          ...policy,
                          maxOsAgeDays: e.target.value === "" ? undefined : parseInt(e.target.value, 10),
                        })}
                        className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                        placeholder="No limit"
                      />
                    </div>
                  </>
                )}
              </div>
            )}

            <div className="mt-5 flex justify-end gap-3">
              <button
                onClick={() => setPolicyAppId(null)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                Cancel
              </button>
              <button
                onClick={savePolicy}
                disabled={policyLoading || policySaving}
                className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
              >
                {policySaving ? "Saving..." : "Save Policy"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Assign dialog modal */}
      {assignAppId && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          {/* Overlay */}
          <div className="fixed inset-0 bg-black/40" onClick={() => setAssignAppId(null)} />
          {/* Modal */}
          <div className="relative w-full max-w-sm rounded-xl bg-white p-6 shadow-2xl dark:bg-slate-900">
            <h3 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Assign Application</h3>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              Select a user to assign this application to.
            </p>

            <div className="mt-4">
              <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">User</label>
              <select
                value={assignUserId}
                onChange={(e) => setAssignUserId(e.target.value)}
                className="mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2.5 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
              >
                <option value="">Select a user...</option>
                {users.map((u) => (
                  <option key={u.id} value={u.keycloakUserId || u.id}>
                    {u.firstName} {u.lastName} ({u.email})
                  </option>
                ))}
              </select>
            </div>

            {assignMessage && (
              <p
                className={`mt-3 text-sm ${
                  assignMessage.type === "success" ? "text-emerald-600" : "text-red-600"
                }`}
              >
                {assignMessage.text}
              </p>
            )}

            <div className="mt-5 flex justify-end gap-3">
              <button
                onClick={() => setAssignAppId(null)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                Cancel
              </button>
              <button
                onClick={handleAssign}
                disabled={!assignUserId || assigning}
                className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
              >
                {assigning ? "Assigning..." : "Assign"}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
