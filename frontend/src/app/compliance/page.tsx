"use client";

import { useEffect, useState } from "react";
import { ShieldCheck, ShieldAlert, AlertCircle, CheckCircle, XCircle, Monitor, RefreshCw, AlertTriangle } from "lucide-react";
import LoadingRows from "@/components/LoadingRows";
import { getOrgCompliance } from "@/lib/api";
import type { ComplianceResponse } from "@/lib/api";
import { useApiReady } from "../providers";

export default function ComplianceDashboardPage() {
  const apiReady = useApiReady();

  const [data, setData] = useState<ComplianceResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // E3: filter to only show devices needing OS update
  const [showNeedsUpdateOnly, setShowNeedsUpdateOnly] = useState(false);

  const fetchData = async () => {
    try {
      setLoading(true);
      setError(null);
      const result = await getOrgCompliance();
      setData(result);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load compliance data");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const pct = (n: number, total: number) =>
    total === 0 ? 0 : Math.round((n / total) * 100);

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Compliance Dashboard</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Security posture across all enrolled devices in your organisation.
          </p>
        </div>
        <button
          onClick={fetchData}
          disabled={loading}
          className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-600 shadow-sm transition-colors hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {loading ? (
        <LoadingRows count={4} className="mt-6 h-24" />
      ) : error ? (
        <div className="mt-6 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      ) : !data ? null : (
        <>
          {/* Summary row */}
          <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-6">
            <SummaryCard label="Total Devices" value={String(data.summary.totalDevices)} sub="" color="slate" />
            <SummaryCard
              label="Compliant"
              value={`${data.summary.compliantDevices}`}
              sub={`${pct(data.summary.compliantDevices, data.summary.totalDevices)}% of fleet`}
              color={data.summary.compliantDevices === data.summary.totalDevices ? "green" : "amber"}
            />
            <SummaryCard
              label="Disk Encrypted"
              value={String(data.summary.encryptedDevices)}
              sub={`${pct(data.summary.encryptedDevices, data.summary.totalDevices)}%`}
              color="blue"
            />
            <SummaryCard
              label="Firewall On"
              value={String(data.summary.firewallEnabled)}
              sub={`${pct(data.summary.firewallEnabled, data.summary.totalDevices)}%`}
              color="blue"
            />
            <SummaryCard
              label="With Vulns"
              value={String(data.summary.devicesWithVulns)}
              sub={data.summary.devicesWithVulns > 0 ? "needs attention" : "clean fleet"}
              color={data.summary.devicesWithVulns > 0 ? "red" : "green"}
            />
            <SummaryCard
              label="Needs Update"
              value={String(data.summary.needsUpdateDevices ?? 0)}
              sub={(data.summary.needsUpdateDevices ?? 0) > 0 ? "OS updates pending" : "up to date"}
              color={(data.summary.needsUpdateDevices ?? 0) > 0 ? "amber" : "green"}
            />
          </div>

          {/* Compliance rate bar */}
          {data.summary.totalDevices > 0 && (
            <div className="mt-6 rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
              <div className="flex items-center justify-between text-sm">
                <span className="font-medium text-slate-700 dark:text-slate-300">Fleet compliance rate</span>
                <span className="font-semibold text-slate-800 dark:text-slate-100">
                  {pct(data.summary.compliantDevices, data.summary.totalDevices)}%
                </span>
              </div>
              <div className="mt-2 h-3 w-full overflow-hidden rounded-full bg-slate-100 dark:bg-slate-700">
                <div
                  className="h-full rounded-full bg-emerald-500 transition-all duration-500"
                  style={{ width: `${pct(data.summary.compliantDevices, data.summary.totalDevices)}%` }}
                />
              </div>
            </div>
          )}

          {/* E3: filter toggle */}
          <div className="mt-4 flex items-center gap-3">
            <button
              onClick={() => setShowNeedsUpdateOnly((v) => !v)}
              className={`flex items-center gap-2 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors ${
                showNeedsUpdateOnly
                  ? "border-amber-300 bg-amber-50 text-amber-700"
                  : "border-slate-200 bg-white text-slate-600 hover:bg-slate-50"
              }`}
            >
              <AlertTriangle className="h-3.5 w-3.5" />
              {showNeedsUpdateOnly ? "Showing: needs update" : "Show only: needs update"}
            </button>
            {showNeedsUpdateOnly && (
              <span className="text-xs text-slate-400">
                {data.devices.filter((d) => d.needsUpdate).length} device(s)
              </span>
            )}
          </div>

          {/* Device table */}
          {data.devices.length === 0 ? (
            <div className="mt-8 rounded-xl border border-dashed border-slate-200 bg-white p-12 text-center dark:border-slate-700 dark:bg-slate-900">
              <Monitor className="mx-auto h-8 w-8 text-slate-300" />
              <p className="mt-3 text-sm text-slate-400 dark:text-slate-500">No devices are enrolled yet.</p>
            </div>
          ) : (
            <div className="mt-4 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-800 dark:text-slate-400">
                      <th className="px-5 py-3">Device</th>
                      <th className="px-5 py-3">OS</th>
                      <th className="px-5 py-3">Disk Enc.</th>
                      <th className="px-5 py-3">Firewall</th>
                      <th className="px-5 py-3">MDM</th>
                      <th className="px-5 py-3">Vulns</th>
                      <th className="px-5 py-3">Update</th>
                      <th className="px-5 py-3">Status</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {data.devices
                      .filter((d) => !showNeedsUpdateOnly || d.needsUpdate)
                      .map((device) => (
                      <tr key={device.deviceId} className="hover:bg-slate-50 transition-colors dark:hover:bg-slate-800">
                        <td className="px-5 py-3">
                          <div className="flex items-center gap-2">
                            <Monitor className="h-4 w-4 text-slate-400 shrink-0" />
                            <span className="font-medium text-slate-800 dark:text-slate-100">
                              {device.hostname || device.deviceId}
                            </span>
                          </div>
                        </td>
                        <td className="px-5 py-3 text-slate-500 dark:text-slate-400">{device.osVersion || "—"}</td>
                        <td className="px-5 py-3">
                          <StatusIcon ok={device.diskEncrypted} />
                        </td>
                        <td className="px-5 py-3">
                          <StatusIcon ok={device.firewallEnabled} />
                        </td>
                        <td className="px-5 py-3">
                          <StatusIcon ok={device.mdmEnrolled} />
                        </td>
                        <td className="px-5 py-3">
                          {device.unknownVulns ? (
                            <span className="text-xs text-amber-600 dark:text-amber-400">Unknown</span>
                          ) : device.vulnerabilities.length > 0 ? (
                            <span className="rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
                              {device.vulnerabilities.length}
                            </span>
                          ) : (
                            <span className="text-xs text-slate-400 dark:text-slate-500">None</span>
                          )}
                        </td>
                        <td className="px-5 py-3">
                          {device.needsUpdate ? (
                            <span className="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-300">
                              <AlertTriangle className="h-3 w-3" />
                              Update
                            </span>
                          ) : (
                            <span className="text-xs text-slate-400 dark:text-slate-500">—</span>
                          )}
                        </td>
                        <td className="px-5 py-3">
                          {device.compliant ? (
                            <span className="inline-flex items-center gap-1 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">
                              <CheckCircle className="h-3.5 w-3.5" />
                              Compliant
                            </span>
                          ) : (
                            <span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2.5 py-0.5 text-xs font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
                              <ShieldAlert className="h-3.5 w-3.5" />
                              Failing
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function SummaryCard({
  label,
  value,
  sub,
  color,
}: {
  label: string;
  value: string;
  sub: string;
  color: "slate" | "green" | "amber" | "red" | "blue";
}) {
  const colors = {
    slate: "bg-slate-50 text-slate-800",
    green: "bg-emerald-50 text-emerald-800",
    amber: "bg-amber-50 text-amber-800",
    red: "bg-red-50 text-red-800",
    blue: "bg-blue-50 text-blue-800",
  };
  return (
    <div className={`rounded-xl border border-slate-200 p-5 dark:border-slate-700 ${colors[color]}`}>
      <p className="text-xs font-medium uppercase tracking-wider opacity-60">{label}</p>
      <p className="mt-1 text-2xl font-bold">{value}</p>
      {sub && <p className="mt-0.5 text-xs opacity-70">{sub}</p>}
    </div>
  );
}

function StatusIcon({ ok }: { ok: boolean }) {
  return ok ? (
    <CheckCircle className="h-4 w-4 text-emerald-500" />
  ) : (
    <XCircle className="h-4 w-4 text-red-400" />
  );
}
