"use client";

import { useEffect, useState } from "react";
import { Link2, Plus, Trash2, RefreshCw, X } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import ConfirmDialog from "@/components/ConfirmDialog";
import {
  listIdentityProviders,
  createIdentityProvider,
  deleteIdentityProvider,
  type IdentityProvider,
  type IdentityProviderRequest,
} from "@/lib/api";
import { useApiReady } from "../../providers";

type ProviderType = IdentityProviderRequest["providerType"];

const PROVIDER_TYPES: { value: ProviderType; label: string }[] = [
  { value: "google", label: "Google" },
  { value: "github", label: "GitHub" },
  { value: "oidc", label: "Generic OIDC" },
  { value: "saml", label: "SAML" },
];

const EMPTY_FORM = {
  alias: "",
  displayName: "",
  providerType: "google" as ProviderType,
  // google / github
  clientId: "",
  clientSecret: "",
  // oidc extras
  authorizationUrl: "",
  tokenUrl: "",
  defaultScope: "",
  // saml
  singleSignOnServiceUrl: "",
  nameIDPolicyFormat: "persistent" as "persistent" | "transient" | "email" | "unspecified",
};

function buildConfig(form: typeof EMPTY_FORM): Record<string, string> {
  switch (form.providerType) {
    case "google":
    case "github":
      return {
        clientId: form.clientId,
        clientSecret: form.clientSecret,
      };
    case "oidc":
      return {
        authorizationUrl: form.authorizationUrl,
        tokenUrl: form.tokenUrl,
        clientId: form.clientId,
        clientSecret: form.clientSecret,
        defaultScope: form.defaultScope,
      };
    case "saml":
      return {
        singleSignOnServiceUrl: form.singleSignOnServiceUrl,
        nameIDPolicyFormat: form.nameIDPolicyFormat,
      };
  }
}

const INPUT_CLS =
  "mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100";
const LABEL_CLS = "block text-sm font-medium text-slate-700 dark:text-slate-300";

export default function IdentityProvidersPage() {
  const apiReady = useApiReady();

  const [providers, setProviders] = useState<IdentityProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({ ...EMPTY_FORM });
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<IdentityProvider | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await listIdentityProviders();
      setProviders(data);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load identity providers");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    load();
  }, [apiReady]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    setSubmitting(true);
    try {
      const req: IdentityProviderRequest = {
        alias: form.alias,
        displayName: form.displayName,
        providerType: form.providerType,
        config: buildConfig(form),
      };
      await createIdentityProvider(req);
      setShowForm(false);
      setForm({ ...EMPTY_FORM });
      await load();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "Failed to create identity provider");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteIdentityProvider(deleteTarget.alias);
      setProviders((prev) => prev.filter((p) => p.alias !== deleteTarget.alias));
      setDeleteTarget(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete identity provider");
    } finally {
      setDeleting(false);
    }
  };

  const set = <K extends keyof typeof EMPTY_FORM>(key: K, value: (typeof EMPTY_FORM)[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Identity Providers</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Configure external identity providers (Google, GitHub, OIDC, SAML) for SSO login.
          </p>
        </div>
        <button
          onClick={() => { setShowForm(true); setFormError(null); setForm({ ...EMPTY_FORM }); }}
          className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-indigo-700"
        >
          <Plus className="h-4 w-4" />
          Add provider
        </button>
      </div>

      {error && <ErrorBanner message={error} onDismiss={() => setError(null)} />}

      {/* Add provider form */}
      {showForm && (
        <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
              New Identity Provider
            </h2>
            <button
              onClick={() => setShowForm(false)}
              className="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {formError && <ErrorBanner message={formError} onDismiss={() => setFormError(null)} />}

          <form onSubmit={handleCreate} className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className={LABEL_CLS}>Alias <span className="text-red-500">*</span></label>
                <input
                  required
                  type="text"
                  value={form.alias}
                  onChange={(e) => set("alias", e.target.value)}
                  placeholder="google-sso"
                  className={INPUT_CLS}
                />
                <p className="mt-0.5 text-xs text-slate-400 dark:text-slate-500">
                  Unique identifier (no spaces)
                </p>
              </div>

              <div>
                <label className={LABEL_CLS}>Display Name</label>
                <input
                  type="text"
                  value={form.displayName}
                  onChange={(e) => set("displayName", e.target.value)}
                  placeholder="Sign in with Google"
                  className={INPUT_CLS}
                />
              </div>

              <div className="sm:col-span-2">
                <label className={LABEL_CLS}>Provider Type <span className="text-red-500">*</span></label>
                <select
                  value={form.providerType}
                  onChange={(e) => set("providerType", e.target.value as ProviderType)}
                  className={INPUT_CLS}
                >
                  {PROVIDER_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
              </div>
            </div>

            {/* Type-specific config fields */}
            {(form.providerType === "google" || form.providerType === "github") && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div>
                  <label className={LABEL_CLS}>Client ID <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="text"
                    value={form.clientId}
                    onChange={(e) => set("clientId", e.target.value)}
                    placeholder="your-client-id"
                    className={INPUT_CLS}
                  />
                </div>
                <div>
                  <label className={LABEL_CLS}>Client Secret <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="password"
                    value={form.clientSecret}
                    onChange={(e) => set("clientSecret", e.target.value)}
                    placeholder="your-client-secret"
                    className={INPUT_CLS}
                  />
                </div>
              </div>
            )}

            {form.providerType === "oidc" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="sm:col-span-2">
                  <label className={LABEL_CLS}>Authorization URL <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="text"
                    value={form.authorizationUrl}
                    onChange={(e) => set("authorizationUrl", e.target.value)}
                    placeholder="https://provider.example.com/oauth2/authorize"
                    className={INPUT_CLS}
                  />
                </div>
                <div className="sm:col-span-2">
                  <label className={LABEL_CLS}>Token URL <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="text"
                    value={form.tokenUrl}
                    onChange={(e) => set("tokenUrl", e.target.value)}
                    placeholder="https://provider.example.com/oauth2/token"
                    className={INPUT_CLS}
                  />
                </div>
                <div>
                  <label className={LABEL_CLS}>Client ID <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="text"
                    value={form.clientId}
                    onChange={(e) => set("clientId", e.target.value)}
                    placeholder="your-client-id"
                    className={INPUT_CLS}
                  />
                </div>
                <div>
                  <label className={LABEL_CLS}>Client Secret <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="password"
                    value={form.clientSecret}
                    onChange={(e) => set("clientSecret", e.target.value)}
                    placeholder="your-client-secret"
                    className={INPUT_CLS}
                  />
                </div>
                <div className="sm:col-span-2">
                  <label className={LABEL_CLS}>Default Scope</label>
                  <input
                    type="text"
                    value={form.defaultScope}
                    onChange={(e) => set("defaultScope", e.target.value)}
                    placeholder="openid profile email"
                    className={INPUT_CLS}
                  />
                </div>
              </div>
            )}

            {form.providerType === "saml" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="sm:col-span-2">
                  <label className={LABEL_CLS}>Single Sign-On Service URL <span className="text-red-500">*</span></label>
                  <input
                    required
                    type="text"
                    value={form.singleSignOnServiceUrl}
                    onChange={(e) => set("singleSignOnServiceUrl", e.target.value)}
                    placeholder="https://idp.example.com/sso/saml"
                    className={INPUT_CLS}
                  />
                </div>
                <div className="sm:col-span-2">
                  <label className={LABEL_CLS}>NameID Policy Format</label>
                  <select
                    value={form.nameIDPolicyFormat}
                    onChange={(e) =>
                      set("nameIDPolicyFormat", e.target.value as typeof form.nameIDPolicyFormat)
                    }
                    className={INPUT_CLS}
                  >
                    <option value="persistent">Persistent</option>
                    <option value="transient">Transient</option>
                    <option value="email">Email</option>
                    <option value="unspecified">Unspecified</option>
                  </select>
                </div>
              </div>
            )}

            <div className="flex justify-end gap-3 pt-2">
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
                {submitting ? "Creating…" : "Create provider"}
              </button>
            </div>
          </form>
        </section>
      )}

      {/* Provider list */}
      {loading ? (
        <div className="space-y-3">
          {[...Array(3)].map((_, i) => (
            <div key={i} className="h-16 w-full animate-pulse rounded-xl bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : providers.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border-2 border-dashed border-slate-300 py-12 text-center dark:border-slate-600">
          <Link2 className="mx-auto h-10 w-10 text-slate-300 dark:text-slate-600" />
          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
            No identity providers configured.
          </p>
          <p className="text-xs text-slate-400 dark:text-slate-500">
            Add a provider to enable social or enterprise SSO.
          </p>
        </div>
      ) : (
        <ul className="divide-y divide-slate-200 rounded-xl border border-slate-200 bg-white shadow-sm dark:divide-slate-700 dark:border-slate-700 dark:bg-slate-900">
          {providers.map((provider) => (
            <li key={provider.alias} className="flex items-center justify-between gap-4 px-6 py-4">
              <div className="flex items-center gap-3">
                <Link2 className="h-5 w-5 shrink-0 text-indigo-500" />
                <div>
                  <p className="font-medium text-slate-800 dark:text-slate-100">
                    {provider.displayName || provider.alias}
                  </p>
                  <p className="text-xs text-slate-500 dark:text-slate-400">
                    {provider.alias} &middot; {provider.providerId}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-3">
                {provider.enabled !== undefined && (
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      provider.enabled
                        ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900 dark:text-emerald-300"
                        : "bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400"
                    }`}
                  >
                    {provider.enabled ? "Enabled" : "Disabled"}
                  </span>
                )}
                <button
                  onClick={() => setDeleteTarget(provider)}
                  title="Delete provider"
                  className="inline-flex items-center gap-1 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:bg-red-50 dark:border-slate-600 dark:hover:bg-red-950"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        isOpen={deleteTarget !== null}
        title="Delete identity provider"
        message={`Remove "${deleteTarget?.displayName || deleteTarget?.alias}"? Users who signed in via this provider may lose access.`}
        confirmLabel="Delete"
        onConfirm={handleDelete}
        onClose={() => setDeleteTarget(null)}
        variant="danger"
      />
    </div>
  );
}
