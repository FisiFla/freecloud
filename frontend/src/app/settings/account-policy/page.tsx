"use client";

import { useEffect, useState } from "react";
import { Shield, Save, RefreshCw, AlertCircle, CheckCircle, ExternalLink } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getAccountPolicy,
  updateAccountPolicy,
  type AccountPolicy,
  type UpdateAccountPolicyRequest,
} from "@/lib/api";
import { useApiReady } from "../../providers";

// Default safe values shown when no policy exists yet.
const DEFAULTS: UpdateAccountPolicyRequest = {
  minLength: 8,
  upperCase: 0,
  lowerCase: 0,
  digits: 0,
  specialChars: 0,
  passwordHistory: 0,
  passwordExpireDays: 0,
  bruteForceProtected: false,
  failureFactor: 30,
  waitIncrementSeconds: 60,
  maxFailureWaitSeconds: 900,
  quickLoginCheckMilliSeconds: 1000,
  minimumQuickLoginWaitSeconds: 60,
  maxDeltaTimeSeconds: 43200,
};

function policyToForm(p: AccountPolicy): UpdateAccountPolicyRequest {
  return {
    minLength: p.minLength,
    upperCase: p.upperCase,
    lowerCase: p.lowerCase,
    digits: p.digits,
    specialChars: p.specialChars,
    passwordHistory: p.passwordHistory,
    passwordExpireDays: p.passwordExpireDays,
    bruteForceProtected: p.bruteForceProtected,
    failureFactor: p.failureFactor,
    waitIncrementSeconds: p.waitIncrementSeconds,
    maxFailureWaitSeconds: p.maxFailureWaitSeconds,
    quickLoginCheckMilliSeconds: p.quickLoginCheckMilliSeconds,
    minimumQuickLoginWaitSeconds: p.minimumQuickLoginWaitSeconds,
    maxDeltaTimeSeconds: p.maxDeltaTimeSeconds,
  };
}

// Minimal number input with label and optional help text.
function NumberField({
  label,
  help,
  value,
  min,
  max,
  onChange,
}: {
  label: string;
  help?: string;
  value: number;
  min: number;
  max: number;
  onChange: (v: number) => void;
}) {
  return (
    <div>
      <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">{label}</label>
      {help && <p className="mt-0.5 text-xs text-slate-400 dark:text-slate-500">{help}</p>}
      <input
        type="number"
        min={min}
        max={max}
        value={value}
        onChange={(e) => {
          const v = parseInt(e.target.value, 10);
          if (!isNaN(v)) onChange(v);
        }}
        className="mt-1 block w-full rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
      />
    </div>
  );
}

export default function AccountPolicyPage() {
  const apiReady = useApiReady();

  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [form, setForm] = useState<UpdateAccountPolicyRequest>(DEFAULTS);

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveSuccess, setSaveSuccess] = useState(false);

  const [rawPolicy, setRawPolicy] = useState<string>("");

  const load = async () => {
    try {
      setLoading(true);
      setFetchError(null);
      const data = await getAccountPolicy();
      setForm(policyToForm(data));
      setRawPolicy(data.passwordPolicy);
    } catch (err: unknown) {
      setFetchError(err instanceof Error ? err.message : "Failed to load account policy");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const set = <K extends keyof UpdateAccountPolicyRequest>(key: K, value: UpdateAccountPolicyRequest[K]) => {
    setSaveSuccess(false);
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      setSaving(true);
      setSaveError(null);
      setSaveSuccess(false);
      const result = await updateAccountPolicy(form);
      setRawPolicy(result.passwordPolicy);
      setSaveSuccess(true);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save account policy");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <div className="flex items-center gap-3">
        <Shield className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Account Policy</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
        Configure realm-wide password requirements and brute-force lockout settings. Changes are applied
        immediately to Keycloak and recorded in the{" "}
        <a
          href="/audit-log"
          className="text-indigo-600 hover:underline dark:text-indigo-400"
        >
          audit log
        </a>
        .
      </p>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
        Self-service password reset is available to users via the{" "}
        <a
          href="/portal"
          className="text-indigo-600 hover:underline dark:text-indigo-400"
        >
          My Portal
        </a>
        {" "}page.
      </p>

      {fetchError && (
        <div className="mt-4">
          <ErrorBanner message={fetchError} />
        </div>
      )}

      {loading ? (
        <div className="mt-6 space-y-4">
          {[...Array(6)].map((_, i) => (
            <div key={i} className="h-10 w-full animate-pulse rounded-lg bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : (
        <form onSubmit={handleSubmit} className="mt-6 space-y-8">
          {/* Password complexity */}
          <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
            <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
              Password Requirements
            </h2>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              Zero means no requirement for that class.
            </p>
            <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <NumberField
                label="Minimum length"
                help="Characters required (0 = unrestricted)"
                value={form.minLength}
                min={0}
                max={256}
                onChange={(v) => set("minLength", v)}
              />
              <NumberField
                label="Uppercase letters"
                help="Minimum number of A–Z"
                value={form.upperCase}
                min={0}
                max={64}
                onChange={(v) => set("upperCase", v)}
              />
              <NumberField
                label="Lowercase letters"
                help="Minimum number of a–z"
                value={form.lowerCase}
                min={0}
                max={64}
                onChange={(v) => set("lowerCase", v)}
              />
              <NumberField
                label="Digits"
                help="Minimum number of 0–9"
                value={form.digits}
                min={0}
                max={64}
                onChange={(v) => set("digits", v)}
              />
              <NumberField
                label="Special characters"
                help="Minimum number of symbols"
                value={form.specialChars}
                min={0}
                max={64}
                onChange={(v) => set("specialChars", v)}
              />
              <NumberField
                label="Password history"
                help="Prevent reuse of last N passwords (0 = off)"
                value={form.passwordHistory}
                min={0}
                max={100}
                onChange={(v) => set("passwordHistory", v)}
              />
              <NumberField
                label="Expiry (days)"
                help="Force reset after N days (0 = never expires)"
                value={form.passwordExpireDays}
                min={0}
                max={3650}
                onChange={(v) => set("passwordExpireDays", v)}
              />
            </div>

            {rawPolicy && (
              <div className="mt-4 rounded-lg border border-slate-100 bg-slate-50 px-4 py-3 dark:border-slate-700 dark:bg-slate-800">
                <p className="text-xs text-slate-500 dark:text-slate-400 font-medium mb-1">Keycloak policy string</p>
                <code className="block text-xs break-all text-slate-700 dark:text-slate-300">
                  {rawPolicy || "(none)"}
                </code>
              </div>
            )}
          </section>

          {/* Brute-force protection */}
          <section className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
            <div className="flex items-center justify-between">
              <div>
                <h2 className="text-base font-semibold text-slate-800 dark:text-slate-100">
                  Brute-Force Protection
                </h2>
                <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
                  Temporarily lock accounts after repeated failed logins.
                </p>
              </div>
              <label className="relative inline-flex cursor-pointer items-center">
                <input
                  type="checkbox"
                  checked={form.bruteForceProtected}
                  onChange={(e) => set("bruteForceProtected", e.target.checked)}
                  className="peer sr-only"
                />
                <div className="peer h-6 w-11 rounded-full bg-slate-200 after:absolute after:left-[2px] after:top-0.5 after:h-5 after:w-5 after:rounded-full after:border after:border-slate-300 after:bg-white after:transition-all after:content-[''] peer-checked:bg-indigo-600 peer-checked:after:translate-x-full peer-checked:after:border-white peer-focus:outline-none dark:border-slate-600 dark:bg-slate-700" />
                <span className="ml-3 text-sm font-medium text-slate-700 dark:text-slate-300">
                  {form.bruteForceProtected ? "Enabled" : "Disabled"}
                </span>
              </label>
            </div>

            {form.bruteForceProtected && (
              <div className="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                <NumberField
                  label="Max login failures"
                  help="Failures before lockout"
                  value={form.failureFactor}
                  min={0}
                  max={1000}
                  onChange={(v) => set("failureFactor", v)}
                />
                <NumberField
                  label="Wait increment (seconds)"
                  help="Added to wait time per failure"
                  value={form.waitIncrementSeconds}
                  min={0}
                  max={86400}
                  onChange={(v) => set("waitIncrementSeconds", v)}
                />
                <NumberField
                  label="Max lockout duration (seconds)"
                  help="Ceiling on the total wait time"
                  value={form.maxFailureWaitSeconds}
                  min={0}
                  max={86400}
                  onChange={(v) => set("maxFailureWaitSeconds", v)}
                />
                <NumberField
                  label="Quick-login check (ms)"
                  help="Detect rapid-fire logins within this window"
                  value={form.quickLoginCheckMilliSeconds}
                  min={0}
                  max={3600000}
                  onChange={(v) => set("quickLoginCheckMilliSeconds", v)}
                />
                <NumberField
                  label="Min quick-login wait (seconds)"
                  help="Minimum wait after quick-login detection"
                  value={form.minimumQuickLoginWaitSeconds}
                  min={0}
                  max={86400}
                  onChange={(v) => set("minimumQuickLoginWaitSeconds", v)}
                />
                <NumberField
                  label="Failure counter reset (seconds)"
                  help="Failure count resets after this period of no failures"
                  value={form.maxDeltaTimeSeconds}
                  min={0}
                  max={2592000}
                  onChange={(v) => set("maxDeltaTimeSeconds", v)}
                />
              </div>
            )}
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
              <span>Account policy saved successfully.</span>
            </div>
          )}

          <div className="flex items-center gap-3">
            <button
              type="submit"
              disabled={saving}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-5 py-2.5 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-600"
            >
              {saving ? (
                <RefreshCw className="h-4 w-4 animate-spin" />
              ) : (
                <Save className="h-4 w-4" />
              )}
              {saving ? "Saving…" : "Save policy"}
            </button>

            <a
              href="/audit-log"
              className="inline-flex items-center gap-1.5 text-sm text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200"
            >
              <ExternalLink className="h-4 w-4" />
              View audit trail
            </a>
          </div>
        </form>
      )}
    </div>
  );
}
