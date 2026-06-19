"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Package, AlertCircle, AlertTriangle, ShieldAlert } from "lucide-react";
import LoadingRows from "@/components/LoadingRows";
import { getUserDeviceSoftware } from "@/lib/api";
import type { DeviceSoftwareResponse } from "@/lib/api";
import { useApiReady } from "../../../providers";

export default function DeviceSoftwarePage() {
  const params = useParams();
  const userId = (params?.id as string) ?? "";
  const apiReady = useApiReady();

  const [data, setData] = useState<DeviceSoftwareResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchData = async () => {
      try {
        setLoading(true);
        setError(null);
        const result = await getUserDeviceSoftware(userId);
        setData(result);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load software inventory");
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [userId, apiReady]);

  return (
    <div>
      <a
        href={`/employees/${userId}`}
        className="mb-4 inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
      >
        &larr; Back to Employee
      </a>

      <div className="mt-4 flex items-center gap-3">
        <Package className="h-6 w-6 text-indigo-500" />
        <h1 className="text-xl font-bold text-slate-800">Software Inventory</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500">
        Installed software and known vulnerabilities across this employee&apos;s devices.
      </p>

      {loading ? (
        <LoadingRows count={3} className="mt-6 h-24" />
      ) : error ? (
        <div className="mt-6 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      ) : !data || data.devices.length === 0 ? (
        <div className="mt-8 rounded-xl border border-dashed border-slate-200 bg-white p-12 text-center">
          <Package className="mx-auto h-8 w-8 text-slate-300" />
          <p className="mt-3 text-sm text-slate-400">No devices found for this employee.</p>
        </div>
      ) : (
        <div className="mt-6 space-y-6">
          {data.devices.map((device) => {
            const vulnCount = device.software.reduce(
              (acc, s) => acc + (s.vulnerabilities?.length ?? 0),
              0
            );
            return (
              <div key={device.deviceId} className="rounded-xl border border-slate-200 bg-white shadow-sm">
                {/* Device header */}
                <div className="flex items-center gap-3 border-b border-slate-100 px-5 py-4">
                  <Package className="h-5 w-5 text-slate-400" />
                  <div className="flex-1">
                    <p className="font-semibold text-slate-800">{device.hostname || device.deviceId}</p>
                    <p className="text-xs text-slate-400">{device.software.length} packages</p>
                  </div>
                  {vulnCount > 0 && (
                    <span className="flex items-center gap-1 rounded-full bg-red-100 px-2.5 py-0.5 text-xs font-medium text-red-700">
                      <ShieldAlert className="h-3.5 w-3.5" />
                      {vulnCount} {vulnCount === 1 ? "vuln" : "vulns"}
                    </span>
                  )}
                </div>

                {device.software.length === 0 ? (
                  <p className="px-5 py-4 text-sm text-slate-400">No software data available.</p>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500">
                          <th className="px-5 py-3">Package</th>
                          <th className="px-5 py-3">Version</th>
                          <th className="px-5 py-3">Vulnerabilities</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-slate-100">
                        {device.software.map((sw, i) => (
                          <tr key={i} className="hover:bg-slate-50 transition-colors">
                            <td className="px-5 py-3 font-medium text-slate-800">{sw.name}</td>
                            <td className="px-5 py-3 font-mono text-xs text-slate-500">{sw.version}</td>
                            <td className="px-5 py-3">
                              {sw.vulnerabilities && sw.vulnerabilities.length > 0 ? (
                                <ul className="space-y-1">
                                  {sw.vulnerabilities.map((v, vi) => (
                                    <li key={vi} className="flex items-start gap-1.5 text-xs text-red-600">
                                      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-400" />
                                      {v}
                                    </li>
                                  ))}
                                </ul>
                              ) : (
                                <span className="text-xs text-slate-400">None</span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
