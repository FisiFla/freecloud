"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { ArrowLeft, Globe, AlertCircle } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import { listAppTemplates, createAppFromTemplate } from "@/lib/api";
import type { AppTemplate, CreateAppResponse } from "@/lib/api";
import { useApiReady } from "../../providers";

export default function AppCatalogPage() {
  const apiReady = useApiReady();
  const [templates, setTemplates] = useState<AppTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Selected template for the setup panel
  const [selected, setSelected] = useState<AppTemplate | null>(null);

  // Form state
  const [appName, setAppName] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [created, setCreated] = useState<CreateAppResponse | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchTemplates = async () => {
      try {
        setLoading(true);
        setError(null);
        const data = await listAppTemplates();
        setTemplates(data);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load templates");
      } finally {
        setLoading(false);
      }
    };
    fetchTemplates();
  }, [apiReady]);

  const openTemplate = (tmpl: AppTemplate) => {
    setSelected(tmpl);
    setAppName(tmpl.name);
    // Reset fields to empty strings keyed by field name
    const initial: Record<string, string> = {};
    tmpl.requiredFields.forEach((f) => { initial[f.name] = ""; });
    setFields(initial);
    setSubmitError(null);
    setCreated(null);
  };

  const closePanel = () => {
    setSelected(null);
    setCreated(null);
    setSubmitError(null);
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selected) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const result = await createAppFromTemplate(selected.id, {
        name: appName.trim(),
        fields,
      });
      setCreated(result);
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : "Failed to create application");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      {/* Header */}
      <div className="flex items-center gap-3">
        <Link
          href="/apps"
          className="flex items-center gap-1.5 text-sm text-slate-500 hover:text-slate-700 transition-colors dark:text-slate-400 dark:hover:text-slate-200"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to Apps
        </Link>
      </div>
      <div className="mt-4">
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Application Catalog</h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Start with a pre-built template to configure SSO quickly.</p>
      </div>

      {/* Error banner */}
      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} onDismiss={() => setError(null)} />
        </div>
      )}

      {/* Template grid */}
      {loading ? (
        <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className="h-48 animate-pulse rounded-xl bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : (
        <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {templates.map((tmpl) => (
            <div
              key={tmpl.id}
              className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition-shadow hover:shadow-md flex flex-col dark:border-slate-700 dark:bg-slate-900"
            >
              <div className="flex items-start justify-between">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600 dark:bg-indigo-950 dark:text-indigo-400">
                  <Globe className="h-5 w-5" />
                </div>
                <span
                  className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    tmpl.protocol === "OIDC"
                      ? "bg-sky-50 text-sky-700"
                      : "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
                  }`}
                >
                  {tmpl.protocol}
                </span>
              </div>

              <h3 className="mt-4 font-semibold text-slate-800 dark:text-slate-100">{tmpl.name}</h3>
              <p className="mt-1 text-sm text-slate-500 flex-1 dark:text-slate-400">{tmpl.description}</p>

              <button
                onClick={() => openTemplate(tmpl)}
                className="mt-4 w-full rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700 dark:bg-indigo-500 dark:hover:bg-indigo-400"
              >
                Configure
              </button>
            </div>
          ))}
          {templates.length === 0 && (
            <div className="col-span-full">
              <EmptyState
                icon={Globe}
                title="No templates available"
                description="No pre-built templates are configured."
              />
            </div>
          )}
        </div>
      )}

      {/* Setup panel (slide-in modal) */}
      {selected && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          {/* Overlay */}
          <div className="fixed inset-0 bg-black/40" onClick={closePanel} />
          {/* Panel */}
          <div className="relative w-full max-w-md rounded-xl bg-white p-6 shadow-2xl overflow-y-auto max-h-[90vh] dark:bg-slate-900">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">{selected.name}</h2>
                <span
                  className={`mt-1 inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    selected.protocol === "OIDC"
                      ? "bg-sky-50 text-sky-700"
                      : "bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
                  }`}
                >
                  {selected.protocol}
                </span>
              </div>
              <button
                onClick={closePanel}
                className="rounded-lg border border-slate-200 px-3 py-1.5 text-sm text-slate-500 hover:bg-slate-50 dark:border-slate-600 dark:text-slate-400 dark:hover:bg-slate-800"
              >
                Close
              </button>
            </div>

            {/* Success state */}
            {created ? (
              <div className="mt-6 space-y-4">
                <div className="rounded-lg border border-emerald-200 bg-emerald-50 p-4 space-y-3 dark:border-emerald-800 dark:bg-emerald-950">
                  <p className="text-sm font-semibold text-emerald-800 dark:text-emerald-300">
                    Application created successfully!
                  </p>
                  {selected.protocol === "SAML" && created.samlEntityId && (
                    <>
                      <div>
                        <p className="text-xs font-medium text-emerald-700 dark:text-emerald-400">SP Entity ID</p>
                        <code className="block mt-1 rounded bg-white px-2 py-1 text-xs text-slate-700 border border-emerald-200 break-all dark:bg-slate-800 dark:text-slate-300 dark:border-emerald-800">
                          {created.samlEntityId}
                        </code>
                      </div>
                      <div>
                        <p className="text-xs font-medium text-emerald-700 dark:text-emerald-400">ACS URL (POST binding)</p>
                        <code className="block mt-1 rounded bg-white px-2 py-1 text-xs text-slate-700 border border-emerald-200 break-all dark:bg-slate-800 dark:text-slate-300 dark:border-emerald-800">
                          {created.samlAcsUrl || "—"}
                        </code>
                      </div>
                    </>
                  )}
                </div>
                <button
                  onClick={closePanel}
                  className="w-full rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700"
                >
                  Done
                </button>
              </div>
            ) : (
              <form onSubmit={handleCreate} className="mt-6 space-y-5">
                {submitError && (
                  <div className="flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                    <AlertCircle className="h-5 w-5 shrink-0" />
                    <span>{submitError}</span>
                  </div>
                )}

                {/* App name */}
                <div>
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">Application Name</label>
                  <input
                    type="text"
                    required
                    value={appName}
                    onChange={(e) => setAppName(e.target.value)}
                    className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
                    placeholder={selected.name}
                  />
                </div>

                {/* Dynamic required fields */}
                {selected.requiredFields.map((field) => (
                  <div key={field.name}>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                      {field.label}
                      {field.required && <span className="ml-1 text-red-500">*</span>}
                    </label>
                    <input
                      type="text"
                      required={field.required}
                      value={fields[field.name] ?? ""}
                      onChange={(e) =>
                        setFields((prev) => ({ ...prev, [field.name]: e.target.value }))
                      }
                      placeholder={field.placeholder ?? ""}
                      className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
                    />
                  </div>
                ))}

                {/* Notes block */}
                {selected.notes && (
                  <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
                    <p className="font-medium">Setup notes</p>
                    <p className="mt-1">{selected.notes}</p>
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
            )}
          </div>
        </div>
      )}
    </div>
  );
}
