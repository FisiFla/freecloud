"use client";

import { useEffect, useState } from "react";
import {
  ShieldCheck,
  KeyRound,
  Fingerprint,
  Trash2,
  RefreshCw,
  Eye,
  EyeOff,
  AlertCircle,
  CheckCircle,
} from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  portalListMFAFactors,
  portalEnrollTOTP,
  portalEnrollWebAuthn,
  portalRemoveMFAFactor,
  portalGetRecoveryCodesStatus,
  portalGenerateRecoveryCodes,
} from "@/lib/api";
import type { MFAFactor, RecoveryCodesStatus } from "@/lib/api";
import { useApiReady } from "../../providers";

export default function SecurityPage() {
  const apiReady = useApiReady();

  // MFA factors
  const [factors, setFactors] = useState<MFAFactor[]>([]);
  const [factorsLoading, setFactorsLoading] = useState(true);
  const [factorsError, setFactorsError] = useState<string | null>(null);

  // Recovery codes
  const [rcStatus, setRcStatus] = useState<RecoveryCodesStatus | null>(null);
  const [rcLoading, setRcLoading] = useState(true);
  const [rcError, setRcError] = useState<string | null>(null);
  const [generatedCodes, setGeneratedCodes] = useState<string[] | null>(null);
  const [codesVisible, setCodesVisible] = useState(false);
  const [generating, setGenerating] = useState(false);

  // Enrollment messages
  const [enrollMsg, setEnrollMsg] = useState<string | null>(null);
  const [enrollError, setEnrollError] = useState<string | null>(null);
  const [enrolling, setEnrolling] = useState<string | null>(null); // "totp" | "webauthn"

  // Remove
  const [removing, setRemoving] = useState<string | null>(null);

  const fetchFactors = async () => {
    try {
      setFactorsLoading(true);
      setFactorsError(null);
      const data = await portalListMFAFactors();
      setFactors(data);
    } catch (err: unknown) {
      setFactorsError(err instanceof Error ? err.message : "Failed to load MFA factors");
    } finally {
      setFactorsLoading(false);
    }
  };

  const fetchRCStatus = async () => {
    try {
      setRcLoading(true);
      setRcError(null);
      const data = await portalGetRecoveryCodesStatus();
      setRcStatus(data);
    } catch (err: unknown) {
      setRcError(err instanceof Error ? err.message : "Failed to load recovery code status");
    } finally {
      setRcLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    fetchFactors();
    fetchRCStatus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const handleEnrollTOTP = async () => {
    try {
      setEnrolling("totp");
      setEnrollMsg(null);
      setEnrollError(null);
      const res = await portalEnrollTOTP();
      setEnrollMsg(res.message ?? "TOTP enrollment initiated. Complete setup on next login.");
      await fetchFactors();
    } catch (err: unknown) {
      setEnrollError(err instanceof Error ? err.message : "Failed to initiate TOTP enrollment");
    } finally {
      setEnrolling(null);
    }
  };

  const handleEnrollWebAuthn = async () => {
    try {
      setEnrolling("webauthn");
      setEnrollMsg(null);
      setEnrollError(null);
      const res = await portalEnrollWebAuthn();
      setEnrollMsg(res.message ?? "WebAuthn enrollment initiated. Complete registration on next login.");
      await fetchFactors();
    } catch (err: unknown) {
      setEnrollError(err instanceof Error ? err.message : "Failed to initiate WebAuthn enrollment");
    } finally {
      setEnrolling(null);
    }
  };

  const handleRemoveFactor = async (credId: string) => {
    if (!confirm("Remove this MFA factor? You may be locked out if this is your only factor.")) return;
    try {
      setRemoving(credId);
      await portalRemoveMFAFactor(credId);
      setFactors((prev) => prev.filter((f) => f.id !== credId));
    } catch (err: unknown) {
      setFactorsError(err instanceof Error ? err.message : "Failed to remove factor");
    } finally {
      setRemoving(null);
    }
  };

  const handleGenerateCodes = async () => {
    if (
      rcStatus?.hasRecoveryCodes &&
      !confirm("This will replace all existing recovery codes. Proceed?")
    ) {
      return;
    }
    try {
      setGenerating(true);
      setRcError(null);
      const res = await portalGenerateRecoveryCodes();
      setGeneratedCodes(res.codes);
      setCodesVisible(true);
      await fetchRCStatus();
    } catch (err: unknown) {
      setRcError(err instanceof Error ? err.message : "Failed to generate recovery codes");
    } finally {
      setGenerating(false);
    }
  };

  const factorTypeLabel = (type: string) => {
    if (type === "otp") return "Authenticator App (TOTP)";
    if (type === "webauthn") return "Passkey / Security Key (WebAuthn)";
    return type;
  };

  const factorIcon = (type: string) =>
    type === "otp" ? (
      <KeyRound className="h-4 w-4 text-indigo-500" />
    ) : (
      <Fingerprint className="h-4 w-4 text-emerald-500" />
    );

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Security</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Manage your multi-factor authentication and recovery codes.
          </p>
        </div>
        <button
          onClick={() => { fetchFactors(); fetchRCStatus(); }}
          disabled={factorsLoading || rcLoading}
          className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 shadow-sm hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800"
        >
          <RefreshCw className={`h-4 w-4 ${factorsLoading || rcLoading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {/* ---------- MFA Factors ---------- */}
      <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <div className="border-b border-slate-100 px-6 py-4 dark:border-slate-800">
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5 text-indigo-500" />
            <h2 className="font-semibold text-slate-800 dark:text-slate-100">MFA Factors</h2>
          </div>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Active second-factor credentials registered on your account.
          </p>
        </div>

        {factorsError && (
          <div className="px-6 pt-4">
            <ErrorBanner message={factorsError} />
          </div>
        )}

        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 text-xs font-medium uppercase text-slate-500 dark:bg-slate-800 dark:text-slate-400">
              <tr>
                <th className="px-6 py-3 text-left">Type</th>
                <th className="px-6 py-3 text-left">Enrolled</th>
                <th className="px-6 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {factorsLoading ? (
                Array.from({ length: 2 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 3 }).map((__, j) => (
                      <td key={j} className="px-6 py-3">
                        <div className="h-4 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : factors.length === 0 ? (
                <tr>
                  <td colSpan={3} className="px-6 py-8 text-center text-slate-400 dark:text-slate-500">
                    No MFA factors enrolled yet.
                  </td>
                </tr>
              ) : (
                factors.map((f) => (
                  <tr key={f.id} className="hover:bg-slate-50 dark:hover:bg-slate-800">
                    <td className="px-6 py-3">
                      <div className="flex items-center gap-2 font-medium text-slate-800 dark:text-slate-100">
                        {factorIcon(f.type)}
                        {factorTypeLabel(f.type)}
                      </div>
                    </td>
                    <td className="px-6 py-3 text-slate-500 dark:text-slate-400">
                      {f.createdDate
                        ? new Date(f.createdDate).toLocaleDateString()
                        : "—"}
                    </td>
                    <td className="px-6 py-3 text-right">
                      <button
                        onClick={() => handleRemoveFactor(f.id)}
                        disabled={removing === f.id}
                        className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-red-600 hover:bg-red-50 disabled:opacity-50 dark:text-red-400 dark:hover:bg-red-950"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        {removing === f.id ? "Removing…" : "Remove"}
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* Enroll buttons */}
        <div className="border-t border-slate-100 px-6 py-4 dark:border-slate-800">
          <p className="mb-3 text-sm font-medium text-slate-700 dark:text-slate-300">
            Add a new factor
          </p>
          <div className="flex flex-wrap gap-3">
            <button
              onClick={handleEnrollTOTP}
              disabled={enrolling === "totp"}
              className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 shadow-sm hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              <KeyRound className="h-4 w-4" />
              {enrolling === "totp" ? "Initiating…" : "Set up Authenticator App"}
            </button>
            <button
              onClick={handleEnrollWebAuthn}
              disabled={enrolling === "webauthn"}
              className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 shadow-sm hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              <Fingerprint className="h-4 w-4" />
              {enrolling === "webauthn" ? "Initiating…" : "Register Passkey / Security Key"}
            </button>
          </div>

          {enrollMsg && (
            <div className="mt-3 flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
              <CheckCircle className="mt-0.5 h-4 w-4 shrink-0" />
              {enrollMsg}
            </div>
          )}
          {enrollError && (
            <div className="mt-3 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              {enrollError}
            </div>
          )}
        </div>
      </div>

      {/* ---------- Recovery Codes ---------- */}
      <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <div className="border-b border-slate-100 px-6 py-4 dark:border-slate-800">
          <div className="flex items-center gap-2">
            <KeyRound className="h-5 w-5 text-amber-500" />
            <h2 className="font-semibold text-slate-800 dark:text-slate-100">Recovery Codes</h2>
          </div>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Single-use backup codes you can use to access your account if you lose your MFA device.
            Store them somewhere safe — they are shown only once.
          </p>
        </div>

        <div className="px-6 py-4">
          {rcError && <ErrorBanner message={rcError} />}

          {!rcLoading && rcStatus && (
            <div className="mb-4 flex items-center gap-2 text-sm text-slate-600 dark:text-slate-400">
              {rcStatus.hasRecoveryCodes ? (
                <>
                  <CheckCircle className="h-4 w-4 text-emerald-500" />
                  {rcStatus.remainingCodeCount} unused recovery code
                  {rcStatus.remainingCodeCount !== 1 ? "s" : ""} available.
                </>
              ) : (
                <>
                  <AlertCircle className="h-4 w-4 text-amber-500" />
                  No recovery codes set up. Generate codes to ensure account recovery access.
                </>
              )}
            </div>
          )}

          {/* Generated codes display */}
          {generatedCodes && (
            <div className="mb-4 rounded-xl border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-950">
              <div className="mb-3 flex items-center justify-between">
                <p className="text-sm font-semibold text-amber-800 dark:text-amber-200">
                  Your recovery codes — save these now!
                </p>
                <button
                  onClick={() => setCodesVisible((v) => !v)}
                  className="flex items-center gap-1 text-xs text-amber-700 hover:text-amber-900 dark:text-amber-300 dark:hover:text-amber-100"
                >
                  {codesVisible ? (
                    <><EyeOff className="h-3.5 w-3.5" /> Hide</>
                  ) : (
                    <><Eye className="h-3.5 w-3.5" /> Show</>
                  )}
                </button>
              </div>
              {codesVisible ? (
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
                  {generatedCodes.map((code) => (
                    <code
                      key={code}
                      className="rounded bg-white px-3 py-1.5 text-center font-mono text-sm font-semibold text-slate-800 shadow-sm dark:bg-slate-900 dark:text-slate-100"
                    >
                      {code}
                    </code>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-amber-700 dark:text-amber-300">
                  {generatedCodes.length} codes hidden. Click Show to reveal.
                </p>
              )}
              <p className="mt-3 text-xs text-amber-600 dark:text-amber-400">
                These codes will not be shown again. Copy and store them securely.
              </p>
            </div>
          )}

          <button
            onClick={handleGenerateCodes}
            disabled={generating}
            className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            <RefreshCw className={`h-4 w-4 ${generating ? "animate-spin" : ""}`} />
            {generating
              ? "Generating…"
              : rcStatus?.hasRecoveryCodes
              ? "Regenerate Codes"
              : "Generate Recovery Codes"}
          </button>
        </div>
      </div>
    </div>
  );
}
