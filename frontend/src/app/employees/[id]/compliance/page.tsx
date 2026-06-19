"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { ShieldCheck, ShieldAlert, AlertCircle, CheckCircle, XCircle, Monitor } from "lucide-react";
import LoadingRows from "@/components/LoadingRows";
import { getUserCompliance } from "@/lib/api";
import type { ComplianceResponse } from "@/lib/api";
import { useApiReady } from "../../../providers";

export default function UserCompliancePage() {
  const params = useParams();
  const userId = (params?.id as string) ?? "";
  const apiReady = useApiReady();

  const [data, setData] = useState<ComplianceResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchData = async () => {
      try {
        setLoading(true);
        setError(null);
        const result = await getUserCompliance(userId);
        setData(result);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load compliance data");
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [userId, apiReady]);

  const pct = (n: number, total: number) =>
    total === 0 ? 0 : Math.round((n / total) * 100);

  return (
    <div>
      <a
        href={`/employees/${userId}`}
        className="mb-4 inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
      >
        &larr; Back to Employee
      </a>

      <div className="mt-4 flex items-center gap-3">
        <ShieldCheck className="h-6 w-6 text-indigo-500" />
        <h1 className="text-xl font-bold text-slate-800">Device Compliance</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500">
        Security posture for this employee&apos;s enrolled devices.
      </p>

      {loading ? (
        <LoadingRows count={2} className="mt-6 h-24" />
      ) : error ? (
        <div className="mt-6 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      ) : !data ? null : (
        <>
          {/* Summary cards */}
          <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <SummaryCard
              label="Total Devices"
              value={String(data.summary.totalDevices)}
              sub=""
              color="slate"
            />
            <SummaryCard
              label="Compliant"
              value={`${data.summary.compliantDevices}/${data.summary.totalDevices}`}
              sub={`${pct(data.summary.compliantDevices, data.summary.totalDevices)}%`}
              color={data.summary.compliantDevices === data.summary.totalDevices ? "green" : "amber"}
            />
            <SummaryCard
              label="Disk Encrypted"
              value={String(data.summary.encryptedDevices)}
              sub={`${pct(data.summary.encryptedDevices, data.summary.totalDevices)}%`}
              color="blue"
            />
            <SummaryCard
              label="With Vulns"
              value={String(data.summary.devicesWithVulns)}
              sub={data.summary.devicesWithVulns > 0 ? "needs attention" : "clean"}
              color={data.summary.devicesWithVulns > 0 ? "red" : "green"}
            />
          </div>

          {/* Per-device detail */}
          {data.devices.length === 0 ? (
            <div className="mt-8 rounded-xl border border-dashed border-slate-200 bg-white p-12 text-center">
              <Monitor className="mx-auto h-8 w-8 text-slate-300" />
              <p className="mt-3 text-sm text-slate-400">No devices found for this employee.</p>
            </div>
          ) : (
            <div className="mt-6 space-y-4">
              {data.devices.map((device) => (
                <div key={device.deviceId} className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex items-center gap-3">
                      <Monitor className="h-5 w-5 text-slate-400 shrink-0" />
                      <div>
                        <p className="font-semibold text-slate-800">{device.hostname || device.deviceId}</p>
                        <p className="text-xs text-slate-400">{device.osVersion || "Unknown OS"}</p>
                      </div>
                    </div>
                    <span
                      className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ${
                        device.compliant
                          ? "bg-emerald-50 text-emerald-700"
                          : "bg-red-50 text-red-700"
                      }`}
                    >
                      {device.compliant ? (
                        <><CheckCircle className="h-3.5 w-3.5" /> Compliant</>
                      ) : (
                        <><ShieldAlert className="h-3.5 w-3.5" /> Non-Compliant</>
                      )}
                    </span>
                  </div>

                  <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
                    <PostureCheck label="Disk Encrypted" ok={device.diskEncrypted} />
                    <PostureCheck label="Firewall" ok={device.firewallEnabled} />
                    <PostureCheck label="MDM Enrolled" ok={device.mdmEnrolled} />
                    <PostureCheck label="No Known Vulns" ok={!device.unknownVulns && device.vulnerabilities.length === 0} />
                  </div>

                  {device.vulnerabilities.length > 0 && (
                    <div className="mt-3 rounded-lg border border-red-100 bg-red-50 px-4 py-3">
                      <p className="text-xs font-semibold text-red-700 mb-1">Vulnerabilities</p>
                      <ul className="space-y-0.5">
                        {device.vulnerabilities.map((v, i) => (
                          <li key={i} className="text-xs text-red-600">{v}</li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {device.unknownVulns && device.vulnerabilities.length === 0 && (
                    <p className="mt-3 text-xs text-amber-600">Vulnerability data unavailable — posture may be inaccurate.</p>
                  )}
                </div>
              ))}
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
    <div className={`rounded-xl border border-slate-200 p-5 ${colors[color]}`}>
      <p className="text-xs font-medium uppercase tracking-wider opacity-60">{label}</p>
      <p className="mt-1 text-2xl font-bold">{value}</p>
      {sub && <p className="mt-0.5 text-xs opacity-70">{sub}</p>}
    </div>
  );
}

function PostureCheck({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div className="flex items-center gap-2 rounded-lg bg-slate-50 px-3 py-2.5">
      {ok ? (
        <CheckCircle className="h-4 w-4 shrink-0 text-emerald-500" />
      ) : (
        <XCircle className="h-4 w-4 shrink-0 text-red-400" />
      )}
      <span className="text-xs font-medium text-slate-700">{label}</span>
    </div>
  );
}
