"use client";

import { useEffect, useState, useCallback } from "react";
import { BarChart2, RefreshCw, TrendingUp, TrendingDown, Minus } from "lucide-react";
import LoadingRows from "@/components/LoadingRows";
import { getAnalyticsSnapshots } from "@/lib/api";
import type { SnapshotRow } from "@/lib/api";
import { useApiReady } from "../providers";

type Window = "7d" | "30d" | "90d" | "custom";

function windowToDates(w: Window): { from: string; to: string } {
  const now = new Date();
  const to = now.toISOString();
  const days = w === "7d" ? 7 : w === "30d" ? 30 : 90;
  const from = new Date(now.getTime() - days * 86400_000).toISOString();
  return { from, to };
}

interface TrendProps {
  current: number;
  previous: number;
  fmt?: (n: number) => string;
}

function TrendBadge({ current, previous, fmt = String }: TrendProps) {
  if (previous === 0 && current === 0) {
    return <span className="inline-flex items-center gap-0.5 text-xs text-slate-400"><Minus className="h-3 w-3" />—</span>;
  }
  const delta = current - previous;
  if (Math.abs(delta) < 1e-9) {
    return <span className="inline-flex items-center gap-0.5 text-xs text-slate-400"><Minus className="h-3 w-3" />0</span>;
  }
  const positive = delta > 0;
  const label = positive ? `+${fmt(Math.abs(delta))}` : `-${fmt(Math.abs(delta))}`;
  return positive ? (
    <span className="inline-flex items-center gap-0.5 text-xs text-green-600 dark:text-green-400">
      <TrendingUp className="h-3 w-3" />{label}
    </span>
  ) : (
    <span className="inline-flex items-center gap-0.5 text-xs text-red-600 dark:text-red-400">
      <TrendingDown className="h-3 w-3" />{label}
    </span>
  );
}

export default function AnalyticsDashboardPage() {
  const apiReady = useApiReady();
  const [rows, setRows] = useState<SnapshotRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [window, setWindow] = useState<Window>("30d");
  // Custom range inputs (ISO date strings like "2026-06-01")
  const [customFrom, setCustomFrom] = useState("");
  const [customTo, setCustomTo] = useState("");

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      let from: string | undefined;
      let to: string | undefined;
      if (window === "custom") {
        if (customFrom) from = new Date(customFrom).toISOString();
        if (customTo) to = new Date(customTo + "T23:59:59Z").toISOString();
      } else {
        const range = windowToDates(window);
        from = range.from;
        to = range.to;
      }
      const data = await getAnalyticsSnapshots(500, from, to);
      setRows(data ?? []);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load analytics");
    } finally {
      setLoading(false);
    }
  }, [window, customFrom, customTo]);

  useEffect(() => {
    if (!apiReady) return;
    fetchData();
  }, [apiReady, fetchData]);

  const pct = (n: number) => `${(n * 100).toFixed(1)}%`;
  const fmtPct = (n: number) => `${(n * 100).toFixed(1)}pp`;

  // Period-over-period: compare first half vs second half of the returned rows.
  const mid = Math.floor(rows.length / 2);
  const prevRows = rows.slice(0, mid);
  const currRows = rows.slice(mid);
  const avg = (arr: SnapshotRow[], key: keyof SnapshotRow) =>
    arr.length === 0 ? 0 : arr.reduce((s, r) => s + (r[key] as number), 0) / arr.length;

  const prevCompliance = avg(prevRows, "complianceRate");
  const currCompliance = avg(currRows, "complianceRate");
  const prevMFA = avg(prevRows, "mfaCoveragePct");
  const currMFA = avg(currRows, "mfaCoveragePct");
  const prevDevices = avg(prevRows, "enrolledDevices");
  const currDevices = avg(currRows, "enrolledDevices");

  const WINDOWS: { label: string; value: Window }[] = [
    { label: "7 days", value: "7d" },
    { label: "30 days", value: "30d" },
    { label: "90 days", value: "90d" },
    { label: "Custom", value: "custom" },
  ];

  return (
    <div>
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 flex items-center gap-2 dark:text-slate-100">
            <BarChart2 className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
            Analytics
          </h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Periodic snapshots of key health metrics over time.
          </p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          {/* Window selector */}
          <div className="flex rounded-lg border border-slate-200 dark:border-slate-700 overflow-hidden text-sm">
            {WINDOWS.map((w) => (
              <button
                key={w.value}
                onClick={() => setWindow(w.value)}
                className={`px-3 py-1.5 font-medium transition-colors ${
                  window === w.value
                    ? "bg-indigo-600 text-white"
                    : "bg-white text-slate-600 hover:bg-slate-50 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800"
                }`}
              >
                {w.label}
              </button>
            ))}
          </div>
          <button
            onClick={fetchData}
            disabled={loading}
            className="flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
            Refresh
          </button>
        </div>
      </div>

      {/* Custom range inputs */}
      {window === "custom" && (
        <div className="mt-4 flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-2">
            <label className="text-sm text-slate-600 dark:text-slate-400">From</label>
            <input
              type="date"
              value={customFrom}
              onChange={(e) => setCustomFrom(e.target.value)}
              className="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200"
            />
          </div>
          <div className="flex items-center gap-2">
            <label className="text-sm text-slate-600 dark:text-slate-400">To</label>
            <input
              type="date"
              value={customTo}
              onChange={(e) => setCustomTo(e.target.value)}
              className="rounded border border-slate-300 px-2 py-1 text-sm dark:border-slate-600 dark:bg-slate-800 dark:text-slate-200"
            />
          </div>
          <button
            onClick={fetchData}
            disabled={loading}
            className="rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
          >
            Apply
          </button>
        </div>
      )}

      {error && (
        <div className="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          {error}
        </div>
      )}

      {/* Period-over-period summary cards */}
      {rows.length >= 2 && (
        <div className="mt-6 grid gap-4 sm:grid-cols-3">
          {[
            {
              label: "Compliance Rate",
              current: currCompliance,
              previous: prevCompliance,
              display: pct(currCompliance),
              fmtDelta: fmtPct,
            },
            {
              label: "MFA Coverage",
              current: currMFA,
              previous: prevMFA,
              display: pct(currMFA),
              fmtDelta: fmtPct,
            },
            {
              label: "Enrolled Devices (avg)",
              current: currDevices,
              previous: prevDevices,
              display: Math.round(currDevices).toString(),
              fmtDelta: (n: number) => Math.round(n).toString(),
            },
          ].map((card) => (
            <div
              key={card.label}
              className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900"
            >
              <p className="text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
                {card.label}
              </p>
              <p className="mt-1 text-2xl font-bold text-slate-800 dark:text-slate-100">{card.display}</p>
              <div className="mt-1">
                <TrendBadge
                  current={card.current}
                  previous={card.previous}
                  fmt={card.fmtDelta}
                />
                <span className="ml-1 text-xs text-slate-400">vs prior half</span>
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="mt-6 overflow-hidden rounded-lg border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm">
            <thead className="bg-slate-50 dark:bg-slate-800">
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
                    className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {loading ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8">
                    <LoadingRows count={6} />
                  </td>
                </tr>
              ) : rows.length === 0 ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-slate-400 dark:text-slate-500">
                    No snapshots in this time window. They are captured every hour once the server is running.
                  </td>
                </tr>
              ) : (
                rows.map((row) => (
                  <tr key={row.id} className="hover:bg-slate-50 transition-colors dark:hover:bg-slate-800">
                    <td className="px-4 py-3 font-mono text-xs text-slate-600 dark:text-slate-400">
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
                    <td className="px-4 py-3 text-slate-700 dark:text-slate-300">{row.enrolledDevices}</td>
                    <td className="px-4 py-3 text-slate-700 dark:text-slate-300">{pct(row.mfaCoveragePct)}</td>
                    <td className="px-4 py-3 text-slate-700 dark:text-slate-300">{row.appCount}</td>
                    <td className="px-4 py-3 text-green-700 font-medium dark:text-green-400">
                      {row.onboardCount > 0 ? `+${row.onboardCount}` : row.onboardCount}
                    </td>
                    <td className="px-4 py-3 text-red-700 font-medium dark:text-red-400">
                      {row.offboardCount > 0 ? `-${row.offboardCount}` : row.offboardCount}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      <p className="mt-3 text-xs text-slate-400 dark:text-slate-500">
        Showing up to 500 snapshots in the selected window. Snapshot interval is configurable via{" "}
        <code className="rounded bg-slate-100 px-1 dark:bg-slate-800 dark:text-slate-300">SNAPSHOT_INTERVAL</code> (default: 1 hour).
      </p>
    </div>
  );
}
