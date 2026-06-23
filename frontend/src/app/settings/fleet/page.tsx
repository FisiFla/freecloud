"use client";

import { useEffect, useState } from "react";
import { Server, Save, RefreshCw, AlertCircle, CheckCircle, Wifi, WifiOff } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getFleetConfig,
  updateFleetConfig,
  testFleetConnection,
  type FleetConfig,
  type UpsertFleetConfigRequest,
  type FleetTestResult,
} from "@/lib/api";
import { useApiReady } from "../../providers";

export default function FleetConfigPage() {
  const apiReady = useApiReady();

  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [config, setConfig] = useState<FleetConfig | null>(null);

  const [serverUrl, setServerUrl] = useState("");
  const [apiToken, setApiToken] = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveSuccess, setSaveSuccess] = useState(false);

  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<FleetTestResult | null>(null);

  const load = async () => {
    try {
      setLoading(true);
      setFetchError(null);
      const data = await getFleetConfig();
      setConfig(data);
      setServerUrl(data.serverUrl);
      setApiToken("");
    } catch (err: unknown) {
      setFetchError(err instanceof Error ? err.message : "Failed to load Fleet configuration");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setSaving(true);
      setSaveError(null);
      setSaveSuccess(false);
      setTestResult(null);
      const req: UpsertFleetConfigRequest = { serverUrl };
      if (apiToken.trim() !== "") req.apiToken = apiToken;
      await updateFleetConfig(req);
      setSaveSuccess(true);
      setApiToken("");
      // Refresh to get updated updatedAt
      const updated = await getFleetConfig();
      setConfig(updated);
      setServerUrl(updated.serverUrl);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save Fleet configuration");
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    try {
      setTesting(true);
      setTestResult(null);
      const result = await testFleetConnection();
      setTestResult(result);
    } catch (err: unknown) {
      setTestResult({ ok: false, error: err instanceof Error ? err.message : "Test failed" });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div>
      <div className="flex items-center gap-3">
        <Server className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Fleet Configuration</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
        Configure the FleetDM server URL and API token used for MDM device management.
      </p>

      {fetchError && (
        <div className="mt-4">
          <ErrorBanner message={fetchError} onDismiss={() => setFetchError(null)} />
        </div>
      )}

      {loading ? (
        <div className="mt-6 space-y-4">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="h-10 w-full animate-pulse rounded-lg bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : (
        <form onSubmit={handleSubmit} className="mt-6 space-y-6">
          <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
            <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
              Connection Settings
            </h2>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              Changes are applied immediately and recorded in the audit log.
            </p>

            <div className="mt-4 space-y-4">
              <div>
                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Server URL
                </label>
                <input
                  type="text"
                  required
                  value={serverUrl}
                  onChange={(e) => { setServerUrl(e.target.value); setSaveSuccess(false); }}
                  placeholder="https://fleet.example.com"
                  className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  API Token
                </label>
                {config?.apiTokenConfigured && apiToken === "" && (
                  <p className="mt-0.5 flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                    <CheckCircle className="h-3.5 w-3.5" />
                    API Token: configured
                  </p>
                )}
                <input
                  type="password"
                  value={apiToken}
                  onChange={(e) => { setApiToken(e.target.value); setSaveSuccess(false); }}
                  placeholder="Leave blank to keep current token"
                  className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                />
              </div>

              {config?.updatedAt && (
                <p className="text-xs text-slate-400 dark:text-slate-500">
                  Last updated: {new Date(config.updatedAt).toLocaleString()}
                </p>
              )}
            </div>
          </section>

          {saveError && (
            <div className="flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
              <AlertCircle className="h-5 w-5 shrink-0" />
              <span>{saveError}</span>
            </div>
          )}

          {saveSuccess && (
            <div className="flex items-center gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
              <CheckCircle className="h-5 w-5 shrink-0" />
              <span>Fleet configuration saved successfully.</span>
            </div>
          )}

          <div className="flex flex-wrap items-center gap-3">
            <button
              type="submit"
              disabled={saving}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {saving ? (
                <RefreshCw className="h-4 w-4 animate-spin" />
              ) : (
                <Save className="h-4 w-4" />
              )}
              {saving ? "Saving…" : "Save configuration"}
            </button>

            <button
              type="button"
              onClick={handleTest}
              disabled={testing}
              className="inline-flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-5 py-2.5 text-sm font-semibold text-slate-700 shadow-sm transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
            >
              {testing ? (
                <RefreshCw className="h-4 w-4 animate-spin" />
              ) : testResult?.ok === false ? (
                <WifiOff className="h-4 w-4 text-red-500" />
              ) : (
                <Wifi className="h-4 w-4" />
              )}
              {testing ? "Testing…" : "Test Connection"}
            </button>
          </div>

          {testResult && (
            <div
              className={`flex items-center gap-3 rounded-lg border px-4 py-3 text-sm ${
                testResult.ok
                  ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
                  : "border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300"
              }`}
            >
              {testResult.ok ? (
                <CheckCircle className="h-5 w-5 shrink-0" />
              ) : (
                <AlertCircle className="h-5 w-5 shrink-0" />
              )}
              <span>{testResult.ok ? "Connection OK" : `Error: ${testResult.error}`}</span>
            </div>
          )}
        </form>
      )}
    </div>
  );
}
