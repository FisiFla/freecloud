"use client";

import { useCallback, useEffect, useState } from "react";
import { Search, Filter, Download } from "lucide-react";
import { listAuditLogs, downloadAuditLogs } from "@/lib/api";
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
  "access_eval",
  "app_policy_upsert",
];

const PAGE_SIZE = 100;

export default function AuditLogPage() {
  const apiReady = useApiReady();
  const [logs, setLogs] = useState<AuditLogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
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

  const fetchPage = useCallback(
    (offset: number) =>
      listAuditLogs({
        actor: debouncedActor || undefined,
        action: actionFilter !== "All Actions" ? actionFilter : undefined,
        limit: PAGE_SIZE,
        offset,
      }),
    [debouncedActor, actionFilter],
  );

  // Reload the first page whenever the filters change.
  useEffect(() => {
    if (!apiReady) return;
    let cancelled = false;
    (async () => {
      try {
        setLoading(true);
        setError(null);
        const data = await fetchPage(0);
        if (cancelled) return;
        setLogs(data);
        setHasMore(data.length === PAGE_SIZE);
      } catch (err: unknown) {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load audit logs");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [fetchPage, apiReady]);

  const loadMore = async () => {
    try {
      setLoadingMore(true);
      setError(null);
      const data = await fetchPage(logs.length);
      setLogs((prev) => [...prev, ...data]);
      setHasMore(data.length === PAGE_SIZE);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load more audit logs");
    } finally {
      setLoadingMore(false);
    }
  };

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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Audit Log</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Track all actions across your FreeCloud instance.</p>
        </div>
        {/* C4: Export buttons — honour current actor/action filters */}
        <div className="flex items-center gap-2">
          <button
            onClick={() =>
              downloadAuditLogs("csv", {
                actor: debouncedActor || undefined,
                action: actionFilter !== "All Actions" ? actionFilter : undefined,
              })
            }
            className="flex items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800"
          >
            <Download className="h-3.5 w-3.5" />
            Export CSV
          </button>
          <button
            onClick={() =>
              downloadAuditLogs("json", {
                actor: debouncedActor || undefined,
                action: actionFilter !== "All Actions" ? actionFilter : undefined,
              })
            }
            className="flex items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-600 transition-colors hover:bg-slate-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800"
          >
            <Download className="h-3.5 w-3.5" />
            Export JSON
          </button>
        </div>
      </div>

      {/* Error banner */}
      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} onDismiss={() => setError(null)} />
        </div>
      )}

      {/* Filters */}
      <div className="mt-6 flex flex-wrap items-end gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <div className="flex-1 min-w-[200px]">
          <label htmlFor="actor-filter" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            Actor
          </label>
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              id="actor-filter"
              type="text"
              placeholder="Search actor..."
              value={actorFilter}
              onChange={(e) => setActorFilter(e.target.value)}
              className="w-full rounded-lg border border-slate-200 bg-white py-2 pl-9 pr-3 text-sm text-slate-700 placeholder-slate-400 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
            />
          </div>
        </div>

        <div className="w-full sm:w-44">
          <label htmlFor="action-filter" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            Action
          </label>
          <div className="relative">
            <Filter className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <select
              id="action-filter"
              value={actionFilter}
              onChange={(e) => setActionFilter(e.target.value)}
              className="w-full appearance-none rounded-lg border border-slate-200 bg-white py-2 pl-9 pr-8 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
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
          <label htmlFor="date-from" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            From
          </label>
          <input
            id="date-from"
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="w-full rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
          />
        </div>

        <div className="w-full sm:w-40">
          <label htmlFor="date-to" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            To
          </label>
          <input
            id="date-to"
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="w-full rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
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
          <div className="mt-4 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-800 dark:text-slate-400">
                    <th className="px-6 py-3">Timestamp</th>
                    <th className="px-6 py-3">Actor</th>
                    <th className="px-6 py-3">Action</th>
                    <th className="px-6 py-3">Target Type</th>
                    <th className="px-6 py-3">Target ID</th>
                    <th className="px-6 py-3">Details</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                  {filtered.map((log) => (
                    <tr key={log.id} className="hover:bg-slate-50 transition-colors dark:hover:bg-slate-800">
                      <td className="whitespace-nowrap px-6 py-4 text-slate-600 font-mono text-xs dark:text-slate-400">
                        {formatTimestamp(log.createdAt)}
                      </td>
                      <td className="px-6 py-4 text-slate-700 dark:text-slate-300">{log.actorId}</td>
                      <td className="px-6 py-4">
                        <span className="inline-flex items-center rounded-full bg-slate-100 px-2.5 py-0.5 text-xs font-medium text-slate-700 dark:bg-slate-700 dark:text-slate-300">
                          {log.action}
                        </span>
                      </td>
                      <td className="px-6 py-4 text-slate-600 capitalize dark:text-slate-400">{log.targetType}</td>
                      <td className="px-6 py-4 font-mono text-xs text-slate-500 dark:text-slate-500">{log.targetId}</td>
                      <td className="px-6 py-4 text-slate-600 max-w-xs truncate dark:text-slate-400">
                        {typeof log.details === "string"
                          ? log.details
                          : JSON.stringify(log.details)}
                      </td>
                    </tr>
                  ))}
                  {filtered.length === 0 && (
                    <tr>
                      <td colSpan={6} className="px-6 py-8 text-center text-sm text-slate-400 dark:text-slate-500">
                        No audit logs match your filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="mt-4 flex items-center justify-between">
            <p className="text-xs text-slate-400 dark:text-slate-500">
              Showing {filtered.length} of {logs.length} loaded event{logs.length === 1 ? "" : "s"}
            </p>
            {hasMore && (
              <button
                onClick={loadMore}
                disabled={loadingMore}
                className="rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-600 transition-colors hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-400 dark:hover:bg-slate-800"
              >
                {loadingMore ? "Loading…" : "Load more"}
              </button>
            )}
          </div>
        </>
      )}
    </div>
  );
}
