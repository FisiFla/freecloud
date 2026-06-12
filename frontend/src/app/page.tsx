"use client";

import { useEffect, useState } from "react";
import { Users, AppWindow, Monitor, ShieldCheck } from "lucide-react";
import { listUsers, listApps, listAuditLogs } from "@/lib/api";

interface StatData {
  label: string;
  value: string;
  icon: React.ComponentType<{ className?: string }>;
  color: string;
}

export default function DashboardPage() {
  const [totalEmployees, setTotalEmployees] = useState<number | null>(null);
  const [connectedApps, setConnectedApps] = useState<number | null>(null);
  const [devicesManaged, setDevicesManaged] = useState<number | null>(null);
  const [recentAuditEvents, setRecentAuditEvents] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetchStats = async () => {
      try {
        setLoading(true);
        const [users, apps, auditLogs] = await Promise.all([
          listUsers(),
          listApps(),
          listAuditLogs({ limit: 100 }),
        ]);
        setTotalEmployees(users.length);
        setConnectedApps(apps.length);

        // Count unique devices from all users
        const deviceCount = users.reduce((count, u) => {
          return count + ((u as any).devices?.length || 0);
        }, 0);
        setDevicesManaged(deviceCount);
        setRecentAuditEvents(auditLogs.length);
      } catch {
        // On error leave nulls so skeletons show
      } finally {
        setLoading(false);
      }
    };
    fetchStats();
  }, []);

  const stats: StatData[] = [
    { label: "Total Employees", value: totalEmployees?.toLocaleString() ?? "—", icon: Users, color: "bg-indigo-500" },
    { label: "Connected Apps", value: connectedApps?.toLocaleString() ?? "—", icon: AppWindow, color: "bg-emerald-500" },
    { label: "Devices Managed", value: devicesManaged?.toLocaleString() ?? "—", icon: Monitor, color: "bg-amber-500" },
    { label: "Recent Audit Events", value: recentAuditEvents?.toLocaleString() ?? "—", icon: ShieldCheck, color: "bg-rose-500" },
  ];

  return (
    <div>
      <h1 className="text-2xl font-bold text-slate-800">Dashboard</h1>
      <p className="mt-1 text-sm text-slate-500">Overview of your FreeCloud instance.</p>

      {/* Stats Grid */}
      <div className="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {stats.map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.label}
              className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm transition-shadow hover:shadow-md"
            >
              <div className="flex items-center justify-between">
                <div className={`flex h-12 w-12 items-center justify-center rounded-lg ${stat.color}`}>
                  <Icon className="h-6 w-6 text-white" />
                </div>
              </div>
              {loading ? (
                <div className="mt-4 h-8 w-20 animate-pulse rounded bg-slate-200" />
              ) : (
                <p className="mt-4 text-2xl font-bold text-slate-800">{stat.value}</p>
              )}
              <p className="mt-1 text-sm text-slate-500">{stat.label}</p>
            </div>
          );
        })}
      </div>

      {/* Welcome Card */}
      <div className="mt-8 rounded-xl border border-slate-200 bg-white p-8 shadow-sm">
        <h2 className="text-lg font-semibold text-slate-800">Welcome to FreeCloud</h2>
        <p className="mt-2 max-w-2xl text-sm text-slate-500 leading-relaxed">
          FreeCloud is your unified identity and access management platform. Manage employees,
          applications, devices, and audit logs — all from a single dashboard. Connect your
          identity provider, configure SSO, and automate onboarding &amp; offboarding workflows.
        </p>
      </div>
    </div>
  );
}
