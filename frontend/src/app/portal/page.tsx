"use client";

import { useEffect, useState } from "react";
import { Monitor, Grid, ShieldCheck, AlertCircle, RefreshCw } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import {
  portalMyDevices,
  portalMyApps,
  portalMyCompliance,
  portalRequestAccess,
} from "@/lib/api";
import type { PortalDevice, PortalApp, ComplianceResponse } from "@/lib/api";
import { useApiReady } from "../providers";

export default function PortalPage() {
  const apiReady = useApiReady();

  const [devices, setDevices] = useState<PortalDevice[]>([]);
  const [apps, setApps] = useState<PortalApp[]>([]);
  const [compliance, setCompliance] = useState<ComplianceResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Access request state
  const [requestAppId, setRequestAppId] = useState("");
  const [requestReason, setRequestReason] = useState("");
  const [requesting, setRequesting] = useState(false);
  const [requestMsg, setRequestMsg] = useState<string | null>(null);
  const [requestError, setRequestError] = useState<string | null>(null);

  const fetchData = async () => {
    try {
      setLoading(true);
      setError(null);
      const [devs, appsData, comp] = await Promise.all([
        portalMyDevices(),
        portalMyApps(),
        portalMyCompliance(),
      ]);
      setDevices(devs);
      setApps(appsData);
      setCompliance(comp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load portal data");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const handleRequestAccess = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!requestAppId.trim()) return;
    try {
      setRequesting(true);
      setRequestMsg(null);
      setRequestError(null);
      await portalRequestAccess(requestAppId.trim(), requestReason.trim());
      setRequestMsg("Access request submitted successfully.");
      setRequestAppId("");
      setRequestReason("");
    } catch (err: unknown) {
      setRequestError(err instanceof Error ? err.message : "Failed to submit request");
    } finally {
      setRequesting(false);
    }
  };

  const compliantCount = compliance?.summary.compliantDevices ?? 0;
  const totalCount = compliance?.summary.totalDevices ?? 0;

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800">My Portal</h1>
          <p className="mt-1 text-sm text-slate-500">
            Your devices, assigned apps, and compliance status.
          </p>
        </div>
        <button
          onClick={fetchData}
          disabled={loading}
          className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 shadow-sm hover:bg-slate-50 disabled:opacity-50"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} />
        </div>
      )}

      {/* Compliance summary */}
      <div className="mt-6 grid gap-4 sm:grid-cols-3">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center gap-2 text-slate-500">
            <Monitor className="h-4 w-4" />
            <span className="text-sm font-medium">My Devices</span>
          </div>
          <p className="mt-2 text-3xl font-bold text-slate-800">
            {loading ? "—" : devices.length}
          </p>
        </div>
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center gap-2 text-slate-500">
            <Grid className="h-4 w-4" />
            <span className="text-sm font-medium">Assigned Apps</span>
          </div>
          <p className="mt-2 text-3xl font-bold text-slate-800">
            {loading ? "—" : apps.length}
          </p>
        </div>
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <div className="flex items-center gap-2 text-slate-500">
            <ShieldCheck className="h-4 w-4" />
            <span className="text-sm font-medium">Compliant Devices</span>
          </div>
          <p className="mt-2 text-3xl font-bold text-slate-800">
            {loading ? "—" : `${compliantCount} / ${totalCount}`}
          </p>
        </div>
      </div>

      {/* Devices table */}
      <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
        <div className="border-b border-slate-100 px-6 py-4">
          <h2 className="font-semibold text-slate-800">My Devices</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 text-xs font-medium uppercase text-slate-500">
              <tr>
                <th className="px-6 py-3 text-left">Hostname</th>
                <th className="px-6 py-3 text-left">OS Version</th>
                <th className="px-6 py-3 text-left">Last Seen</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {loading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 3 }).map((__, j) => (
                      <td key={j} className="px-6 py-3">
                        <div className="h-4 animate-pulse rounded bg-slate-200" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : devices.length === 0 ? (
                <tr>
                  <td colSpan={3} className="px-6 py-8 text-center text-slate-400">
                    No devices enrolled.
                  </td>
                </tr>
              ) : (
                devices.map((d) => (
                  <tr key={d.fleetHostId} className="hover:bg-slate-50">
                    <td className="px-6 py-3 font-medium text-slate-800">{d.hostname || d.fleetHostId}</td>
                    <td className="px-6 py-3 text-slate-600">{d.osVersion || "—"}</td>
                    <td className="px-6 py-3 text-slate-600">
                      {d.lastSeenAt ? new Date(d.lastSeenAt).toLocaleString() : "—"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Assigned apps */}
      <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
        <div className="border-b border-slate-100 px-6 py-4">
          <h2 className="font-semibold text-slate-800">My Apps</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-50 text-xs font-medium uppercase text-slate-500">
              <tr>
                <th className="px-6 py-3 text-left">Name</th>
                <th className="px-6 py-3 text-left">Protocol</th>
                <th className="px-6 py-3 text-left">URL</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {loading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 3 }).map((__, j) => (
                      <td key={j} className="px-6 py-3">
                        <div className="h-4 animate-pulse rounded bg-slate-200" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : apps.length === 0 ? (
                <tr>
                  <td colSpan={3} className="px-6 py-8 text-center text-slate-400">
                    No apps assigned.
                  </td>
                </tr>
              ) : (
                apps.map((a) => (
                  <tr key={a.id} className="hover:bg-slate-50">
                    <td className="px-6 py-3 font-medium text-slate-800">{a.name}</td>
                    <td className="px-6 py-3 text-slate-600">{a.protocol}</td>
                    <td className="px-6 py-3 text-slate-600">
                      {a.baseUrl ? (
                        <a
                          href={a.baseUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-indigo-600 hover:underline"
                        >
                          {a.baseUrl}
                        </a>
                      ) : (
                        "—"
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Access request form */}
      <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        <h2 className="font-semibold text-slate-800">Request App Access</h2>
        <p className="mt-1 text-sm text-slate-500">
          Submit a request for an app you need access to. An administrator will review it.
        </p>
        <form onSubmit={handleRequestAccess} className="mt-4 space-y-3">
          <div>
            <label htmlFor="appId" className="block text-sm font-medium text-slate-700">
              App ID (UUID)
            </label>
            <input
              id="appId"
              type="text"
              value={requestAppId}
              onChange={(e) => setRequestAppId(e.target.value)}
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              required
            />
          </div>
          <div>
            <label htmlFor="reason" className="block text-sm font-medium text-slate-700">
              Reason (optional)
            </label>
            <input
              id="reason"
              type="text"
              value={requestReason}
              onChange={(e) => setRequestReason(e.target.value)}
              placeholder="Why do you need access?"
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
          </div>
          {requestMsg && (
            <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-2 text-sm text-emerald-700">
              {requestMsg}
            </div>
          )}
          {requestError && (
            <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {requestError}
            </div>
          )}
          <button
            type="submit"
            disabled={requesting || !requestAppId.trim()}
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {requesting ? "Submitting…" : "Submit Request"}
          </button>
        </form>
      </div>
    </div>
  );
}
