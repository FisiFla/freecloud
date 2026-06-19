"use client";

import { useEffect, useState } from "react";
import { BarChart2, RefreshCw } from "lucide-react";
import LoadingRows from "@/components/LoadingRows";
import { getAnalyticsSnapshots } from "@/lib/api";
import type { SnapshotRow } from "@/lib/api";
import { useApiReady } from "../providers";

export default function AnalyticsDashboardPage() {
  const apiReady = useApiReady();
  const [rows, setRows] = useState<SnapshotRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await getAnalyticsSnapshots(48);
      setRows(data ?? []);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load analytics");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const pct = (n: number) => `${(n * 100).toFixed(1)}%`;

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 flex items-center gap-2">
            <BarChart2 className="h-6 w-6 text-indigo-600" />
            Analytics
          </h1>
          <p className="mt-1 text-sm text-slate-500">
            Periodic snapshots of key health metrics over time.
          </p>
        </div>
        <button
          onClick={fetchData}
          disabled={loading}
          className="flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {error && (
        <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      <div className="mt-6 overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm">
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm">
            <thead className="bg-slate-50">
              <tr>
                {[
                  "Captured At",
                  "Compliance",
                  "Enrolled Devices",
                  "MFA Coverage",
                  "Apps",
                  "Onboards",
                  "Offboards",
                ].map((h) => (
                  <th
                    key={h}
                    className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {loading ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8">
                    <LoadingRows count={6} />
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-slate-400">
                    No snapshots yet. They are captured every hour once the server is running.
                  </td>
                </tr>
              ) : (
                rows.map((row) => (
                  <tr key={row.id} className="hover:bg-slate-50 transition-colors">
                    <td className="px-4 py-3 font-mono text-xs text-slate-600">
                      {new Date(row.capturedAt).toLocaleString()}
                    </td>
                    <td className="px-4 py-3">
                      <span
                        className={`font-medium ${
                          row.complianceRate >= 0.9
                            ? "text-green-600"
                            : row.complianceRate >= 0.7
                            ? "text-amber-600"
                            : "text-red-600"
                        }`}
                      >
                        {pct(row.complianceRate)}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-slate-700">{row.enrolledDevices}</td>
                    <td className="px-4 py-3 text-slate-700">{pct(row.mfaCoveragePct)}</td>
                    <td className="px-4 py-3 text-slate-700">{row.appCount}</td>
                    <td className="px-4 py-3 text-green-700 font-medium">
                      {row.onboardCount > 0 ? `+${row.onboardCount}` : row.onboardCount}
                    </td>
                    <td className="px-4 py-3 text-red-700 font-medium">
                      {row.offboardCount > 0 ? `-${row.offboardCount}` : row.offboardCount}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      <p className="mt-3 text-xs text-slate-400">
        Showing up to 48 snapshots, oldest first. Snapshot interval is configurable via{" "}
        <code className="rounded bg-slate-100 px-1">SNAPSHOT_INTERVAL</code> (default: 1 hour).
      </p>
    </div>
  );
}
