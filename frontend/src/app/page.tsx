"use client";

import { useEffect, useState } from "react";
import {
  Users,
  AppWindow,
  Monitor,
  ShieldCheck,
  AlertCircle,
  TrendingUp,
  TrendingDown,
  Minus,
  ShieldAlert,
  KeyRound,
  RefreshCcw,
} from "lucide-react";
import { listUsers, listApps, listAuditLogs, getAnalyticsSnapshots, getOrgCompliance } from "@/lib/api";
import type { SnapshotRow } from "@/lib/api";
import { useApiReady } from "./providers";

// Trend direction for a metric (higher is better unless `lowerIsBetter`).
type TrendDir = "up" | "down" | "flat";

function trendDir(current: number, previous: number | null): TrendDir {
  if (previous === null || Math.abs(current - previous) < 1e-9) return "flat";
  return current > previous ? "up" : "down";
}

interface TrendArrowProps {
  dir: TrendDir;
  /** When true, up = bad (red), down = good (green). */
  lowerIsBetter?: boolean;
}
function TrendArrow({ dir, lowerIsBetter = false }: TrendArrowProps) {
  if (dir === "flat")
    return <Minus className="h-4 w-4 text-slate-400" />;
  const isGood = lowerIsBetter ? dir === "down" : dir === "up";
  const cls = isGood ? "text-green-500" : "text-red-500";
  return dir === "up"
    ? <TrendingUp className={`h-4 w-4 ${cls}`} />
    : <TrendingDown className={`h-4 w-4 ${cls}`} />;
}

export default function DashboardPage() {
  const apiReady = useApiReady();

  // Basic stats
  const [totalEmployees, setTotalEmployees] = useState<number | null>(null);
  const [connectedApps, setConnectedApps] = useState<number | null>(null);
  const [devicesManaged, setDevicesManaged] = useState<number | null>(null);
  const [recentAuditEvents, setRecentAuditEvents] = useState<number | null>(null);

  // Compliance/MFA metrics from analytics snapshots
  const [latestSnapshot, setLatestSnapshot] = useState<SnapshotRow | null>(null);
  const [prevSnapshot, setPrevSnapshot] = useState<SnapshotRow | null>(null);

  // Needs-update device count from compliance endpoint
  const [needsUpdateCount, setNeedsUpdateCount] = useState<number | null>(null);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchStats = async () => {
      try {
        setLoading(true);
        setError(null);
        const [users, apps, auditLogs, snapshots, complianceRes] = await Promise.all([
          listUsers(),
          listApps(),
          listAuditLogs({ limit: 100 }),
          getAnalyticsSnapshots(2).catch(() => [] as SnapshotRow[]),
          getOrgCompliance().catch(() => null),
        ]);
        setTotalEmployees(users.length);
        setConnectedApps(apps.length);

        // Count unique devices from all users
        const deviceCount = users.reduce((count, u) => count + (u.devices?.length || 0), 0);
        setDevicesManaged(deviceCount);
        setRecentAuditEvents(auditLogs.length);

        // Analytics snapshots: newest is last (oldest-first ordering from API)
        if (snapshots.length >= 1) {
          setLatestSnapshot(snapshots[snapshots.length - 1]);
        }
        if (snapshots.length >= 2) {
          setPrevSnapshot(snapshots[snapshots.length - 2]);
        }

        // Needs-update count from compliance summary
        if (complianceRes) {
          setNeedsUpdateCount(complianceRes.summary.needsUpdateDevices);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load dashboard data");
      } finally {
        setLoading(false);
      }
    };
    fetchStats();
  }, [apiReady]);

  const pct = (n: number) => `${(n * 100).toFixed(1)}%`;

  // Compliance rate: stored as 0–1 in the snapshot.
  const complianceRate = latestSnapshot?.complianceRate ?? null;
  const prevComplianceRate = prevSnapshot?.complianceRate ?? null;
  const complianceTrend = complianceRate !== null && prevComplianceRate !== null
    ? trendDir(complianceRate, prevComplianceRate)
    : "flat";

  // MFA coverage: stored as 0–100 in the snapshot (percentage points).
  const mfaCoverage = latestSnapshot?.mfaCoveragePct ?? null;
  const prevMfaCoverage = prevSnapshot?.mfaCoveragePct ?? null;
  const mfaTrend = mfaCoverage !== null && prevMfaCoverage !== null
    ? trendDir(mfaCoverage, prevMfaCoverage)
    : "flat";

  return (
    <div>
      <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Dashboard</h1>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Overview of your FreeCloud instance.</p>

      {/* Error banner */}
      {error && !loading && (
        <div className="mt-4 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          <AlertCircle className="h-5 w-5 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {/* Primary Stats Grid */}
      <div className="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {[
          { label: "Total Employees", value: totalEmployees?.toLocaleString() ?? "—", icon: Users, color: "bg-indigo-500" },
          { label: "Connected Apps", value: connectedApps?.toLocaleString() ?? "—", icon: AppWindow, color: "bg-emerald-500" },
          { label: "Devices Managed", value: devicesManaged?.toLocaleString() ?? "—", icon: Monitor, color: "bg-amber-500" },
          { label: "Recent Audit Events", value: recentAuditEvents?.toLocaleString() ?? "—", icon: ShieldCheck, color: "bg-rose-500" },
        ].map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.label}
              className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm transition-shadow hover:shadow-md dark:border-slate-700 dark:bg-slate-900"
            >
              <div className="flex items-center justify-between">
                <div className={`flex h-12 w-12 items-center justify-center rounded-lg ${stat.color}`}>
                  <Icon className="h-6 w-6 text-white" />
                </div>
              </div>
              {loading ? (
                <div className="mt-4 h-8 w-20 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
              ) : (
                <p className="mt-4 text-2xl font-bold text-slate-800 dark:text-slate-100">{stat.value}</p>
              )}
              <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">{stat.label}</p>
            </div>
          );
        })}
      </div>

      {/* Security Health Metrics (from analytics snapshots) */}
      <h2 className="mt-10 text-base font-semibold text-slate-700 dark:text-slate-300">Security Health</h2>
      <div className="mt-3 grid gap-6 sm:grid-cols-3">
        {/* Compliance Rate */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
          <div className="flex items-center justify-between">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-green-100 dark:bg-green-900">
              <ShieldCheck className="h-5 w-5 text-green-600 dark:text-green-400" />
            </div>
            {!loading && complianceRate !== null && (
              <TrendArrow dir={complianceTrend} />
            )}
          </div>
          {loading ? (
            <div className="mt-3 h-8 w-20 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
          ) : (
            <p
              className={`mt-3 text-2xl font-bold ${
                (complianceRate ?? 0) >= 0.9
                  ? "text-green-600 dark:text-green-400"
                  : (complianceRate ?? 0) >= 0.7
                  ? "text-amber-600 dark:text-amber-400"
                  : "text-red-600 dark:text-red-400"
              }`}
            >
              {complianceRate !== null ? pct(complianceRate) : "—"}
            </p>
          )}
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Compliance Rate</p>
          {!loading && prevComplianceRate !== null && complianceRate !== null && (
            <p className="mt-0.5 text-xs text-slate-400">
              prev: {pct(prevComplianceRate)}
            </p>
          )}
        </div>

        {/* MFA Coverage */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
          <div className="flex items-center justify-between">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-100 dark:bg-indigo-900">
              <KeyRound className="h-5 w-5 text-indigo-600 dark:text-indigo-400" />
            </div>
            {!loading && mfaCoverage !== null && (
              <TrendArrow dir={mfaTrend} />
            )}
          </div>
          {loading ? (
            <div className="mt-3 h-8 w-20 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
          ) : (
            <p
              className={`mt-3 text-2xl font-bold ${
                (mfaCoverage ?? 0) >= 90
                  ? "text-green-600 dark:text-green-400"
                  : (mfaCoverage ?? 0) >= 60
                  ? "text-amber-600 dark:text-amber-400"
                  : "text-red-600 dark:text-red-400"
              }`}
            >
              {mfaCoverage !== null ? `${mfaCoverage.toFixed(1)}%` : "—"}
            </p>
          )}
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">MFA Coverage</p>
          {!loading && prevMfaCoverage !== null && mfaCoverage !== null && (
            <p className="mt-0.5 text-xs text-slate-400">
              prev: {prevMfaCoverage.toFixed(1)}%
            </p>
          )}
        </div>

        {/* Devices Needing OS Update */}
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900">
          <div className="flex items-center justify-between">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-amber-100 dark:bg-amber-900">
              <RefreshCcw className="h-5 w-5 text-amber-600 dark:text-amber-400" />
            </div>
            {!loading && needsUpdateCount !== null && (
              <TrendArrow
                dir={needsUpdateCount > 0 ? "up" : "flat"}
                lowerIsBetter
              />
            )}
          </div>
          {loading ? (
            <div className="mt-3 h-8 w-20 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
          ) : (
            <p
              className={`mt-3 text-2xl font-bold ${
                (needsUpdateCount ?? 0) === 0
                  ? "text-green-600 dark:text-green-400"
                  : (needsUpdateCount ?? 0) <= 3
                  ? "text-amber-600 dark:text-amber-400"
                  : "text-red-600 dark:text-red-400"
              }`}
            >
              {needsUpdateCount !== null ? needsUpdateCount.toLocaleString() : "—"}
            </p>
          )}
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Devices Need OS Update</p>
          {!loading && needsUpdateCount === null && (
            <p className="mt-0.5 text-xs text-slate-400">Fleet data unavailable</p>
          )}
        </div>
      </div>

      {/* Placeholder alert for high needs-update count */}
      {!loading && (needsUpdateCount ?? 0) > 0 && (
        <div className="mt-4 flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300">
          <ShieldAlert className="h-5 w-5 shrink-0 mt-0.5" />
          <span>
            <span className="font-semibold">{needsUpdateCount}</span> device{needsUpdateCount !== 1 ? "s" : ""} pending OS update.
            Visit the Compliance page for details.
          </span>
        </div>
      )}

      {/* Welcome Card */}
      <div className="mt-8 rounded-xl border border-slate-200 bg-white p-8 shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <h2 className="text-lg font-semibold text-slate-800 dark:text-slate-100">Welcome to FreeCloud</h2>
        <p className="mt-2 max-w-2xl text-sm text-slate-500 leading-relaxed dark:text-slate-400">
          FreeCloud is your unified identity and access management platform. Manage employees,
          applications, devices, and audit logs — all from a single dashboard. Connect your
          identity provider, configure SSO, and automate onboarding &amp; offboarding workflows.
        </p>
      </div>
    </div>
  );
}
