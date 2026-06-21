"use client";

import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { ShieldX, ArrowLeft } from "lucide-react";

const remediationHints: Record<string, string> = {
  disk_not_encrypted: "Enable FileVault (macOS) or BitLocker (Windows)",
  firewall_disabled: "Enable the system firewall in Security settings",
  vulnerability: "Install pending software updates",
  vulnerability_data_missing: "Install pending software updates",
  no_enrolled_device: "Enroll your device through IT",
};

function getHint(reason: string): string {
  for (const key of Object.keys(remediationHints)) {
    if (reason.includes(key)) return remediationHints[key];
  }
  return "Contact IT support";
}

function AccessBlockedContent() {
  const searchParams = useSearchParams();
  const rawReasons = searchParams.get("reasons") ?? "";
  const reasons = rawReasons
    .split(",")
    .map((r) => r.trim())
    .filter(Boolean);

  return (
    <div className="min-h-screen bg-slate-50 flex items-center justify-center p-6 dark:bg-slate-950">
      <div className="w-full max-w-md">
        <div className="rounded-xl border border-red-200 bg-white p-8 shadow-sm text-center dark:border-red-800 dark:bg-slate-900">
          <div className="flex justify-center">
            <div className="flex h-16 w-16 items-center justify-center rounded-full bg-red-50 text-red-500 dark:bg-red-950 dark:text-red-400">
              <ShieldX className="h-8 w-8" />
            </div>
          </div>

          <h1 className="mt-5 text-2xl font-bold text-slate-800 dark:text-slate-100">Device Not Compliant</h1>
          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
            Access has been blocked because your device does not meet security requirements.
          </p>

          {reasons.length > 0 && (
            <div className="mt-6 text-left space-y-3">
              {reasons.map((reason, i) => (
                <div
                  key={i}
                  className="rounded-lg border border-slate-200 bg-slate-50 p-4 dark:border-slate-700 dark:bg-slate-800"
                >
                  <p className="text-sm font-medium text-slate-700 capitalize dark:text-slate-300">
                    {reason.replace(/_/g, " ")}
                  </p>
                  <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">{getHint(reason)}</p>
                </div>
              ))}
            </div>
          )}

          <div className="mt-6">
            <button
              onClick={() => window.history.back()}
              className="flex w-full items-center justify-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              <ArrowLeft className="h-4 w-4" />
              Try Again
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export default function AccessBlockedPage() {
  return (
    <Suspense fallback={<div className="min-h-screen bg-slate-50 dark:bg-slate-950" />}>
      <AccessBlockedContent />
    </Suspense>
  );
}
