"use client";

import { useEffect, useState } from "react";
import { Plus, Globe, AlertCircle, UserPlus } from "lucide-react";
import SlideOver from "@/components/SlideOver";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import { listApps, createApp, listUsers, assignAppToUser, waitForAuthToken } from "@/lib/api";
import type { App, User } from "@/lib/api";

export default function AppsPage() {
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

  // Assign dialog state
  const [assignAppId, setAssignAppId] = useState<string | null>(null);
  const [assignUserId, setAssignUserId] = useState("");
  const [assigning, setAssigning] = useState(false);
  const [assignMessage, setAssignMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);

  useEffect(() => {
    const fetchApps = async () => {
      try {
        setLoading(true);
        setError(null);
        await waitForAuthToken();
        const data = await listApps();
        setApps(data);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load apps");
      } finally {
        setLoading(false);
      }
    };
    fetchApps();
  }, []);

  useEffect(() => {
    const fetchUsers = async () => {
      try {
        await waitForAuthToken();
        const data = await listUsers();
        setUsers(Array.isArray(data) ? data : []);
      } catch {
        // Silently ignore — users are optional for the assign flow
      }
    };
    fetchUsers();
  }, []);

  const handleAddApp = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setSubmitError(null);
    try {
      const created = await createApp({
        name: newName,
        protocol: newProtocol,
        redirectURIs: newRedirectUris.split("\n").map((s) => s.trim()).filter(Boolean),
        baseURL: newBaseUrl,
      });
      setApps((prev) => [...prev, created]);
      setShowAdd(false);
      setNewName("");
      setNewProtocol("OIDC");
      setNewRedirectUris("");
      setNewBaseUrl("");
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create app");
    } finally {
      setSubmitting(false);
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
            <h1 className="text-2xl font-bold text-slate-800">App Catalog</h1>
            <p className="mt-1 text-sm text-slate-500">Manage SSO-connected applications.</p>
          </div>
          <button
            onClick={() => setShowAdd(true)}
            className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
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
              <div key={i} className="h-40 animate-pulse rounded-xl bg-slate-200" />
            ))}
          </div>
        ) : (
          /* Grid */
          <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {apps.map((app) => (
              <div
                key={app.id}
                className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition-shadow hover:shadow-md"
              >
                <div className="flex items-start justify-between">
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                    <Globe className="h-5 w-5" />
                  </div>
                </div>

                <h3 className="mt-4 font-semibold text-slate-800">{app.name}</h3>
                <p className="mt-1 text-xs text-slate-500 truncate">{app.baseUrl}</p>

                <div className="mt-4 flex items-center gap-2">
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      app.protocol === "OIDC"
                        ? "bg-sky-50 text-sky-700"
                        : "bg-amber-50 text-amber-700"
                    }`}
                  >
                    {app.protocol}
                  </span>
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      app.enabled
                        ? "bg-emerald-50 text-emerald-700"
                        : "bg-slate-100 text-slate-500"
                    }`}
                  >
                    {app.enabled ? "Enabled" : "Disabled"}
                  </span>
                </div>

                {/* Assign button */}
                <button
                  onClick={() => { setAssignAppId(app.id); setAssignUserId(""); setAssignMessage(null); }}
                  className="mt-4 flex w-full items-center justify-center gap-1.5 rounded-lg border border-slate-200 px-3 py-2 text-xs font-medium text-slate-600 transition-colors hover:bg-indigo-50 hover:text-indigo-700 hover:border-indigo-200"
                >
                  <UserPlus className="h-3.5 w-3.5" />
                  Assign
                </button>
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
      <SlideOver isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add Application">
        <form onSubmit={handleAddApp} className="space-y-5">
          {submitError && (
            <div className="flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              <AlertCircle className="h-5 w-5 shrink-0" />
              <span>{submitError}</span>
            </div>
          )}
          <div>
            <label className="block text-sm font-medium text-slate-700">Application Name</label>
            <input
              type="text"
              required
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="My App"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700">Protocol</label>
            <div className="mt-2 flex gap-4">
              <label className="flex items-center gap-2 text-sm text-slate-700">
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
              <label className="flex items-center gap-2 text-sm text-slate-700">
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
            <label className="block text-sm font-medium text-slate-700">
              Redirect URIs <span className="text-slate-400">(one per line)</span>
            </label>
            <textarea
              rows={4}
              value={newRedirectUris}
              onChange={(e) => setNewRedirectUris(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="https://myapp.com/callback"
            />
            {newProtocol === "OIDC" && !newRedirectUris.trim() && (
              <p className="mt-1 text-xs text-amber-600">At least one redirect URI is required for OIDC apps</p>
            )}
            {newRedirectUris.trim() && (
              <p className="mt-1 text-xs text-slate-400">Redirect URIs must start with https:// or http://localhost</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700">Base URL</label>
            <input
              type="url"
              required
              value={newBaseUrl}
              onChange={(e) => setNewBaseUrl(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="https://myapp.com"
            />
          </div>

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {submitting ? "Creating..." : "Create Application"}
          </button>
        </form>
      </SlideOver>

      {/* Assign dialog modal */}
      {assignAppId && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          {/* Overlay */}
          <div className="fixed inset-0 bg-black/40" onClick={() => setAssignAppId(null)} />
          {/* Modal */}
          <div className="relative w-full max-w-sm rounded-xl bg-white p-6 shadow-2xl">
            <h3 className="text-lg font-semibold text-slate-800">Assign Application</h3>
            <p className="mt-1 text-sm text-slate-500">
              Select a user to assign this application to.
            </p>

            <div className="mt-4">
              <label className="block text-sm font-medium text-slate-700">User</label>
              <select
                value={assignUserId}
                onChange={(e) => setAssignUserId(e.target.value)}
                className="mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2.5 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
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
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50"
              >
                Cancel
              </button>
              <button
                onClick={handleAssign}
                disabled={!assignUserId || assigning}
                className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
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
