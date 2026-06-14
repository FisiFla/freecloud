"use client";

import { useEffect, useState } from "react";
import { Search, Filter } from "lucide-react";
import { listAuditLogs } from "@/lib/api";
import type { AuditLogEntry } from "@/lib/api";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import LoadingRows from "@/components/LoadingRows";
import { useApiReady } from "../providers";

const actionOptions = [
  "All Actions",
  "onboard",
  "offboard",
  "app_create",
  "app_assign",
];

export default function AuditLogPage() {
  const apiReady = useApiReady();
  const [logs, setLogs] = useState<AuditLogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [actorFilter, setActorFilter] = useState("");
  const [actionFilter, setActionFilter] = useState("All Actions");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");

  // Debounce the actor filter so we don't fire a request per keystroke.
  const [debouncedActor, setDebouncedActor] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedActor(actorFilter), 300);
    return () => clearTimeout(t);
  }, [actorFilter]);

  useEffect(() => {
    if (!apiReady) return;
    const fetchLogs = async () => {
      try {
        setLoading(true);
        setError(null);
        const data = await listAuditLogs({
          actor: debouncedActor || undefined,
          action: actionFilter !== "All Actions" ? actionFilter : undefined,
          limit: 100,
        });
        setLogs(data);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load audit logs");
      } finally {
        setLoading(false);
      }
    };
    fetchLogs();
  }, [debouncedActor, actionFilter, apiReady]);

  // Actor and action filters are applied server-side via listAuditLogs().
  // Only the date-range filter is applied client-side here (not yet supported
  // by the backend API). This avoids filtering the same dimension twice.
  const filtered = logs.filter((log) => {
    if (dateFrom && new Date(log.createdAt) < new Date(dateFrom)) return false;
    if (dateTo && new Date(log.createdAt) > new Date(dateTo + "T23:59:59Z")) return false;
    return true;
  });

  const formatTimestamp = (ts: string) => {
    const d = new Date(ts);
    return d.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  return (
    <div>
      <h1 className="text-2xl font-bold text-slate-800">Audit Log</h1>
      <p className="mt-1 text-sm text-slate-500">Track all actions across your FreeCloud instance.</p>

      {/* Error banner */}
      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} onDismiss={() => setError(null)} />
        </div>
      )}

      {/* Filters */}
      <div className="mt-6 flex flex-wrap items-end gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
        <div className="flex-1 min-w-[200px]">
          <label className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1">
            Actor
          </label>
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              type="text"
              placeholder="Search actor..."
              value={actorFilter}
              onChange={(e) => setActorFilter(e.target.value)}
              className="w-full rounded-lg border border-slate-200 bg-white py-2 pl-9 pr-3 text-sm text-slate-700 placeholder-slate-400 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
          </div>
        </div>

        <div className="w-full sm:w-44">
          <label className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1">
            Action
          </label>
          <div className="relative">
            <Filter className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <select
              value={actionFilter}
              onChange={(e) => setActionFilter(e.target.value)}
              className="w-full appearance-none rounded-lg border border-slate-200 bg-white py-2 pl-9 pr-8 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            >
              {actionOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="w-full sm:w-40">
          <label className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1">
            From
          </label>
          <input
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="w-full rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          />
        </div>

        <div className="w-full sm:w-40">
          <label className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1">
            To
          </label>
          <input
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="w-full rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          />
        </div>
      </div>

      {/* Loading skeleton */}
      {loading ? (
        <LoadingRows count={4} className="h-12" />
      ) : logs.length === 0 ? (
        <EmptyState
          icon={Search}
          title="No audit events found"
          description="Actions will appear here as you onboard employees and manage apps."
        />
      ) : (
        <>
          {/* Table */}
          <div className="mt-4 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500">
                    <th className="px-6 py-3">Timestamp</th>
                    <th className="px-6 py-3">Actor</th>
                    <th className="px-6 py-3">Action</th>
                    <th className="px-6 py-3">Target Type</th>
                    <th className="px-6 py-3">Target ID</th>
                    <th className="px-6 py-3">Details</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {filtered.map((log) => (
                    <tr key={log.id} className="hover:bg-slate-50 transition-colors">
                      <td className="whitespace-nowrap px-6 py-4 text-slate-600 font-mono text-xs">
                        {formatTimestamp(log.createdAt)}
                      </td>
                      <td className="px-6 py-4 text-slate-700">{log.actorId}</td>
                      <td className="px-6 py-4">
                        <span className="inline-flex items-center rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-medium text-slate-700">
                          {log.action}
                        </span>
                      </td>
                      <td className="px-6 py-4 text-slate-600 capitalize">{log.targetType}</td>
                      <td className="px-6 py-4 font-mono text-xs text-slate-500">{log.targetId}</td>
                      <td className="px-6 py-4 text-slate-600 max-w-xs truncate">
                        {typeof log.details === "string"
                          ? log.details
                          : JSON.stringify(log.details)}
                      </td>
                    </tr>
                  ))}
                  {filtered.length === 0 && (
                    <tr>
                      <td colSpan={6} className="px-6 py-8 text-center text-sm text-slate-400">
                        No audit logs match your filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <p className="mt-4 text-xs text-slate-400">
            Showing {filtered.length} of {logs.length} events
          </p>
        </>
      )}
    </div>
  );
}
