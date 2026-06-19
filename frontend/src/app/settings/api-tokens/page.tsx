"use client";

import { useEffect, useState } from "react";
import { Key, Trash2, AlertCircle, Copy, Check } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import ConfirmDialog from "@/components/ConfirmDialog";
import { listAPITokens, createAPIToken, revokeAPIToken } from "@/lib/api";
import type { APIToken, CreateAPITokenRequest } from "@/lib/api";
import { useApiReady } from "../../providers";

const TOKEN_ROLES = ["super-admin", "helpdesk", "auditor", "read-only"] as const;

export default function APITokensPage() {
  const apiReady = useApiReady();

  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create form state
  const [name, setName] = useState("");
  const [role, setRole] = useState<string>(TOKEN_ROLES[0]);
  const [serviceIdentity, setServiceIdentity] = useState("");
  const [expiresInDays, setExpiresInDays] = useState(0);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [newToken, setNewToken] = useState<APIToken | null>(null);
  const [copied, setCopied] = useState(false);

  // Revoke dialog
  const [revokeTarget, setRevokeTarget] = useState<APIToken | null>(null);
  const [revoking, setRevoking] = useState(false);

  const fetchTokens = async () => {
    try {
      setLoading(true);
      setError(null);
      const result = await listAPITokens();
      setTokens(result);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load API tokens");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    fetchTokens();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !serviceIdentity.trim()) return;
    const req: CreateAPITokenRequest = {
      name: name.trim(),
      role,
      serviceIdentity: serviceIdentity.trim(),
      expiresInDays,
    };
    try {
      setCreating(true);
      setCreateError(null);
      setNewToken(null);
      const created = await createAPIToken(req);
      setNewToken(created);
      setName("");
      setServiceIdentity("");
      setExpiresInDays(0);
      await fetchTokens();
    } catch (err: unknown) {
      setCreateError(err instanceof Error ? err.message : "Failed to create token");
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    try {
      setRevoking(true);
      await revokeAPIToken(revokeTarget.id);
      setRevokeTarget(null);
      await fetchTokens();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to revoke token");
      setRevokeTarget(null);
    } finally {
      setRevoking(false);
    }
  };

  const copyToClipboard = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard write failed silently
    }
  };

  return (
    <div>
      <div className="flex items-center gap-3">
        <Key className="h-6 w-6 text-indigo-600" />
        <h1 className="text-2xl font-bold text-slate-800">API Tokens</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500">
        Create scoped service-account tokens for automated integrations. Tokens
        are shown only once — store them securely.
      </p>

      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} />
        </div>
      )}

      {/* New token banner — shown right after creation */}
      {newToken?.token && (
        <div className="mt-6 rounded-xl border border-emerald-200 bg-emerald-50 p-5">
          <p className="text-sm font-semibold text-emerald-800">
            Token created — copy it now. It will not be shown again.
          </p>
          <div className="mt-3 flex items-center gap-2">
            <code className="flex-1 overflow-x-auto rounded-lg bg-white px-3 py-2 font-mono text-sm text-slate-800 border border-emerald-200 select-all">
              {newToken.token}
            </code>
            <button
              onClick={() => copyToClipboard(newToken.token!)}
              className="flex items-center gap-1 rounded-lg border border-emerald-200 bg-white px-3 py-2 text-sm text-emerald-700 hover:bg-emerald-50"
              aria-label="Copy token"
            >
              {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
        </div>
      )}

      {/* Create form */}
      <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        <h2 className="font-semibold text-slate-800">Create New Token</h2>
        <form onSubmit={handleCreate} className="mt-4 grid gap-4 sm:grid-cols-2">
          <div>
            <label className="block text-sm font-medium text-slate-700">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. CI pipeline token"
              required
              maxLength={100}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-700">Role</label>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            >
              {TOKEN_ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-700">Service Identity</label>
            <input
              type="text"
              value={serviceIdentity}
              onChange={(e) => setServiceIdentity(e.target.value)}
              placeholder="e.g. github-actions"
              required
              maxLength={100}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-slate-700">
              Expires in days <span className="text-slate-400">(0 = never)</span>
            </label>
            <input
              type="number"
              min={0}
              value={expiresInDays}
              onChange={(e) => setExpiresInDays(Number(e.target.value))}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
          </div>
          {createError && (
            <div className="col-span-2 flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {createError}
            </div>
          )}
          <div className="col-span-2">
            <button
              type="submit"
              disabled={creating || !name.trim() || !serviceIdentity.trim()}
              className="rounded-lg bg-indigo-600 px-5 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {creating ? "Creating…" : "Create Token"}
            </button>
          </div>
        </form>
      </div>

      {/* Token list */}
      <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
        <div className="border-b border-slate-100 px-6 py-4">
          <h2 className="font-semibold text-slate-800">Active Tokens</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 text-xs font-medium uppercase text-slate-500">
              <tr>
                <th className="px-6 py-3 text-left">Name</th>
                <th className="px-6 py-3 text-left">Role</th>
                <th className="px-6 py-3 text-left">Service Identity</th>
                <th className="px-6 py-3 text-left">Created</th>
                <th className="px-6 py-3 text-left">Expires</th>
                <th className="px-6 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {loading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 6 }).map((__, j) => (
                      <td key={j} className="px-6 py-3">
                        <div className="h-4 animate-pulse rounded bg-slate-200" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : tokens.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                    No active tokens. Create one above.
                  </td>
                </tr>
              ) : (
                tokens.map((t) => (
                  <tr key={t.id} className="hover:bg-slate-50">
                    <td className="px-6 py-3 font-medium text-slate-800">{t.name}</td>
                    <td className="px-6 py-3">
                      <span className="inline-flex items-center rounded-full bg-indigo-50 px-2.5 py-0.5 text-xs font-medium text-indigo-700">
                        {t.role}
                      </span>
                    </td>
                    <td className="px-6 py-3 text-slate-600">{t.serviceIdentity}</td>
                    <td className="px-6 py-3 text-slate-600">
                      {new Date(t.createdAt).toLocaleDateString()}
                    </td>
                    <td className="px-6 py-3 text-slate-600">
                      {t.expiresAt ? new Date(t.expiresAt).toLocaleDateString() : "Never"}
                    </td>
                    <td className="px-6 py-3 text-right">
                      <button
                        onClick={() => setRevokeTarget(t)}
                        className="inline-flex items-center gap-1 rounded-lg border border-red-200 bg-red-50 px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-100"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        Revoke
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Revoke confirm dialog */}
      <ConfirmDialog
        isOpen={!!revokeTarget}
        title="Revoke API Token"
        message={`Revoke "${revokeTarget?.name}"? Services using this token will lose access immediately.`}
        confirmLabel={revoking ? "Revoking…" : "Revoke"}
        variant="danger"
        onConfirm={handleRevoke}
        onClose={() => setRevokeTarget(null)}
      />
    </div>
  );
}
