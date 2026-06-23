"use client";

import { useEffect, useState } from "react";
import { Mail, Save, RefreshCw, AlertCircle, CheckCircle, Send } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getSMTPConfig,
  updateSMTPConfig,
  sendTestEmail,
  type SMTPConfig,
  type UpsertSMTPConfigRequest,
} from "@/lib/api";
import { useApiReady } from "../../providers";

export default function SMTPConfigPage() {
  const apiReady = useApiReady();

  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [config, setConfig] = useState<SMTPConfig | null>(null);

  const [host, setHost] = useState("");
  const [port, setPort] = useState("587");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [fromAddress, setFromAddress] = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveSuccess, setSaveSuccess] = useState(false);

  const [testEmail, setTestEmail] = useState("");
  const [sendingTest, setSendingTest] = useState(false);
  const [testResult, setTestResult] = useState<{ sent: boolean; error?: string } | null>(null);

  const load = async () => {
    try {
      setLoading(true);
      setFetchError(null);
      const data = await getSMTPConfig();
      setConfig(data);
      setHost(data.host);
      setPort(data.port || "587");
      setUsername(data.username);
      setFromAddress(data.fromAddress);
      setPassword("");
    } catch (err: unknown) {
      setFetchError(err instanceof Error ? err.message : "Failed to load SMTP configuration");
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
      const req: UpsertSMTPConfigRequest = { host, port, username, fromAddress };
      if (password.trim() !== "") req.password = password;
      await updateSMTPConfig(req);
      setSaveSuccess(true);
      setPassword("");
      const updated = await getSMTPConfig();
      setConfig(updated);
      setHost(updated.host);
      setPort(updated.port || "587");
      setUsername(updated.username);
      setFromAddress(updated.fromAddress);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save SMTP configuration");
    } finally {
      setSaving(false);
    }
  };

  const handleSendTest = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setSendingTest(true);
      setTestResult(null);
      const result = await sendTestEmail(testEmail);
      setTestResult(result);
    } catch (err: unknown) {
      setTestResult({ sent: false, error: err instanceof Error ? err.message : "Send failed" });
    } finally {
      setSendingTest(false);
    }
  };

  return (
    <div>
      <div className="flex items-center gap-3">
        <Mail className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">SMTP Configuration</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
        Configure the outbound mail server used for password resets, notifications, and alerts.
      </p>

      {fetchError && (
        <div className="mt-4">
          <ErrorBanner message={fetchError} onDismiss={() => setFetchError(null)} />
        </div>
      )}

      {loading ? (
        <div className="mt-6 space-y-4">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-10 w-full animate-pulse rounded-lg bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : (
        <div className="mt-6 space-y-6">
          <form onSubmit={handleSubmit} className="space-y-6">
            <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
              <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
                Server Settings
              </h2>
              <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
                Credentials are stored encrypted. Leave password blank to keep the current value.
              </p>

              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <div className="sm:col-span-2">
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Host
                  </label>
                  <input
                    type="text"
                    required
                    value={host}
                    onChange={(e) => { setHost(e.target.value); setSaveSuccess(false); }}
                    placeholder="smtp.example.com"
                    className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Port
                  </label>
                  <input
                    type="text"
                    required
                    value={port}
                    onChange={(e) => { setPort(e.target.value); setSaveSuccess(false); }}
                    placeholder="587"
                    className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Username
                  </label>
                  <input
                    type="text"
                    value={username}
                    onChange={(e) => { setUsername(e.target.value); setSaveSuccess(false); }}
                    placeholder="smtp-user@example.com"
                    className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  />
                </div>

                <div className="sm:col-span-2">
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Password
                  </label>
                  {config?.passwordConfigured && password === "" && (
                    <p className="mt-0.5 flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                      <CheckCircle className="h-3.5 w-3.5" />
                      Password: configured
                    </p>
                  )}
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => { setPassword(e.target.value); setSaveSuccess(false); }}
                    placeholder="Leave blank to keep current password"
                    className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  />
                </div>

                <div className="sm:col-span-2">
                  <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    From Address
                  </label>
                  <input
                    type="email"
                    required
                    value={fromAddress}
                    onChange={(e) => { setFromAddress(e.target.value); setSaveSuccess(false); }}
                    placeholder="noreply@example.com"
                    className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  />
                </div>

                {config?.updatedAt && (
                  <p className="sm:col-span-2 text-xs text-slate-400 dark:text-slate-500">
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
                <span>SMTP configuration saved successfully.</span>
              </div>
            )}

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
          </form>

          {/* Test email section */}
          <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
            <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
              Send Test Email
            </h2>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              Verify the SMTP configuration by sending a test message.
            </p>
            <form onSubmit={handleSendTest} className="mt-4 flex flex-wrap items-end gap-3">
              <div className="flex-1 min-w-48">
                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                  Recipient
                </label>
                <input
                  type="email"
                  required
                  value={testEmail}
                  onChange={(e) => { setTestEmail(e.target.value); setTestResult(null); }}
                  placeholder="you@example.com"
                  className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                />
              </div>
              <button
                type="submit"
                disabled={sendingTest}
                className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {sendingTest ? (
                  <RefreshCw className="h-4 w-4 animate-spin" />
                ) : (
                  <Send className="h-4 w-4" />
                )}
                {sendingTest ? "Sending…" : "Send"}
              </button>
            </form>

            {testResult && (
              <div
                className={`mt-3 flex items-center gap-3 rounded-lg border px-4 py-3 text-sm ${
                  testResult.sent
                    ? "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
                    : "border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300"
                }`}
              >
                {testResult.sent ? (
                  <CheckCircle className="h-5 w-5 shrink-0" />
                ) : (
                  <AlertCircle className="h-5 w-5 shrink-0" />
                )}
                <span>{testResult.sent ? "Test email sent" : `Failed: ${testResult.error}`}</span>
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
