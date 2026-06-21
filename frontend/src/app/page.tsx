"use client";

import { useEffect, useState } from "react";
import { Users, AppWindow, Monitor, ShieldCheck, AlertCircle } from "lucide-react";
import { listUsers, listApps, listAuditLogs } from "@/lib/api";
import { useApiReady } from "./providers";

interface StatData {
  label: string;
  value: string;
  icon: React.ComponentType<{ className?: string }>;
  color: string;
}

export default function DashboardPage() {
  const apiReady = useApiReady();
  const [totalEmployees, setTotalEmployees] = useState<number | null>(null);
  const [connectedApps, setConnectedApps] = useState<number | null>(null);
  const [devicesManaged, setDevicesManaged] = useState<number | null>(null);
  const [recentAuditEvents, setRecentAuditEvents] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchStats = async () => {
      try {
        setLoading(true);
        setError(null);
        const [users, apps, auditLogs] = await Promise.all([
          listUsers(),
          listApps(),
          listAuditLogs({ limit: 100 }),
        ]);
        setTotalEmployees(users.length);
        setConnectedApps(apps.length);

        // Count unique devices from all users
        const deviceCount = users.reduce((count, u) => {
          return count + (u.devices?.length || 0);
        }, 0);
        setDevicesManaged(deviceCount);
        setRecentAuditEvents(auditLogs.length);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load dashboard data");
      } finally {
        setLoading(false);
      }
    };
    fetchStats();
  }, [apiReady]);

  const stats: StatData[] = [
    { label: "Total Employees", value: totalEmployees?.toLocaleString() ?? "—", icon: Users, color: "bg-indigo-500" },
    { label: "Connected Apps", value: connectedApps?.toLocaleString() ?? "—", icon: AppWindow, color: "bg-emerald-500" },
    { label: "Devices Managed", value: devicesManaged?.toLocaleString() ?? "—", icon: Monitor, color: "bg-amber-500" },
    { label: "Recent Audit Events", value: recentAuditEvents?.toLocaleString() ?? "—", icon: ShieldCheck, color: "bg-rose-500" },
  ];

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

      {/* Stats Grid */}
      <div className="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {stats.map((stat) => {
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
