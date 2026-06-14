"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Mail, Briefcase, Building2, Monitor, AlertTriangle, AlertCircle, CheckCircle, XCircle } from "lucide-react";
import ConfirmDialog from "@/components/ConfirmDialog";
import { offboardUser, getUser } from "@/lib/api";
import type { OffboardResponse, Device } from "@/lib/api";
import { useApiReady } from "../../providers";

interface EmployeeDetail {
  id: string;
  firstName: string;
  lastName: string;
  email: string;
  department: string;
  role: string;
  disabled: boolean;
}

export default function EmployeeDetailPage() {
  const params = useParams();
  const userId = (params?.id as string) ?? "";
  const apiReady = useApiReady();

  const [employee, setEmployee] = useState<EmployeeDetail | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [showConfirm, setShowConfirm] = useState(false);
  const [deprovisioning, setDeprovisioning] = useState(false);
  const [deprovisioned, setDeprovisioned] = useState(false);
  const [offboardResult, setOffboardResult] = useState<OffboardResponse | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchData = async () => {
      try {
        setLoading(true);
        setError(null);

        const userData = await getUser(userId);
        setEmployee({
          id: String(userData.id || ""),
          firstName: String(userData.firstName || ""),
          lastName: String(userData.lastName || ""),
          email: String(userData.email || ""),
          department: String(userData.department || ""),
          role: String(userData.role || ""),
          disabled: Boolean(userData.disabled),
        });
        // Read the viewed user's real devices from the user record, rather
        // than calling checkDevice() (which checks the *current* logged-in
        // user's devices, not this employee's).
        setDevices(Array.isArray(userData.devices) ? userData.devices : []);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load employee data");
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [userId, apiReady]);

  const handleDeprovision = async () => {
    setDeprovisioning(true);
    setActionError(null);
    try {
      const result = await offboardUser(userId);
      setOffboardResult(result);
      setDeprovisioned(true);
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : "Failed to deprovision. Check the backend is running.");
    } finally {
      setDeprovisioning(false);
    }
  };

  if (loading) {
    return (
      <div>
        <div className="mb-4 h-5 w-32 animate-pulse rounded bg-slate-200" />
        <div className="mt-4 h-40 animate-pulse rounded-xl bg-slate-200" />
        <div className="mt-6 h-24 animate-pulse rounded-xl bg-slate-200" />
      </div>
    );
  }

  if (error) {
    return (
      <div>
        <a
          href="/employees"
          className="mb-4 inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
        >
          &larr; Back to Employees
        </a>
        <div className="mt-8 text-center rounded-xl border border-dashed border-slate-200 bg-white p-12">
          <AlertCircle className="mx-auto h-8 w-8 text-red-300" />
          <h3 className="mt-3 text-sm font-medium text-red-600">User not found</h3>
          <p className="mt-1 text-sm text-slate-400">This employee may have been removed or the ID is invalid.</p>
          <a href="/employees" className="mt-4 inline-block text-sm font-medium text-indigo-600 hover:text-indigo-800">
            &larr; Back to Employees
          </a>
        </div>
      </div>
    );
  }

  if (!employee) {
    return (
      <div>
        <a
          href="/employees"
          className="mb-4 inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
        >
          &larr; Back to Employees
        </a>
        <p className="mt-4 text-slate-500">Employee not found.</p>
      </div>
    );
  }

  return (
    <div>
      {/* Back link */}
      <a
        href="/employees"
        className="mb-4 inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
      >
        &larr; Back to Employees
      </a>

      {deprovisioned ? (
        <div className="mt-8 space-y-4">
          {/* Warning banner if sessionTerminationError exists */}
          {offboardResult?.sessionTerminationError && (
            <div className="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-amber-800">
              <AlertTriangle className="h-5 w-5 shrink-0 text-amber-600" />
              <div>
                <p className="font-medium">Session termination warning</p>
                <p className="text-sm text-amber-700">{offboardResult.sessionTerminationError}</p>
              </div>
            </div>
          )}

          {/* Warnings banner */}
          {offboardResult?.warnings && offboardResult.warnings.length > 0 && (
            <div className="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-amber-800">
              <AlertTriangle className="h-5 w-5 shrink-0 text-amber-600" />
              <div>
                <p className="font-medium">Offboarding warnings</p>
                <ul className="mt-1 list-inside list-disc text-sm text-amber-700">
                  {offboardResult.warnings.map((w, i) => <li key={i}>{w}</li>)}
                </ul>
              </div>
            </div>
          )}

          {/* Result panel */}
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex items-center gap-3">
              <AlertTriangle className="h-8 w-8 text-red-500" />
              <div>
                <h2 className="text-lg font-semibold text-slate-800">Account Deprovisioned</h2>
                <p className="text-sm text-slate-500">
                  {employee.firstName} {employee.lastName} has been deprovisioned.
                </p>
              </div>
            </div>

            <div className="mt-5 space-y-3 border-t border-slate-100 pt-4">
              {/* Sessions terminated */}
              <div className="flex items-center justify-between rounded-lg bg-slate-50 px-4 py-3">
                <span className="text-sm text-slate-700">Sessions terminated</span>
                {offboardResult?.sessionsTerminated ? (
                  <span className="flex items-center gap-1.5 text-sm font-medium text-emerald-700">
                    <CheckCircle className="h-4 w-4" />
                    Yes
                  </span>
                ) : (
                  <span className="flex items-center gap-1.5 text-sm font-medium text-red-600">
                    <XCircle className="h-4 w-4" />
                    No
                  </span>
                )}
              </div>

              {/* Devices wiped */}
              <div className="flex items-center justify-between rounded-lg bg-slate-50 px-4 py-3">
                <span className="text-sm text-slate-700">Devices wiped</span>
                <span className="text-sm font-medium text-slate-800">
                  {offboardResult?.devicesWiped ?? 0}
                </span>
              </div>

              {/* Devices failed */}
              <div className="flex items-center justify-between rounded-lg bg-slate-50 px-4 py-3">
                <span className="text-sm text-slate-700">Devices failed</span>
                <span className={`text-sm font-medium ${(offboardResult?.devicesFailed ?? 0) > 0 ? "text-red-600" : "text-slate-800"}`}>
                  {offboardResult?.devicesFailed ?? 0}
                </span>
              </div>
            </div>
          </div>

          <a
            href="/employees"
            className="inline-flex items-center text-sm text-indigo-600 hover:text-indigo-800"
          >
            &larr; Back to Employees
          </a>
        </div>
      ) : (
        <>
          {/* Profile Card */}
          <div className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex items-start gap-5">
              <div className="flex h-14 w-14 items-center justify-center rounded-full bg-indigo-100 text-lg font-bold text-indigo-700">
                {employee.firstName[0]}
                {employee.lastName[0]}
              </div>
              <div className="flex-1">
                <h1 className="text-xl font-bold text-slate-800">
                  {employee.firstName} {employee.lastName}
                </h1>
                <div className="mt-3 space-y-2 text-sm text-slate-500">
                  <div className="flex items-center gap-2">
                    <Mail className="h-4 w-4" />
                    {employee.email}
                  </div>
                  <div className="flex items-center gap-2">
                    <Briefcase className="h-4 w-4" />
                    {employee.role}
                  </div>
                  <div className="flex items-center gap-2">
                    <Building2 className="h-4 w-4" />
                    {employee.department}
                  </div>
                </div>
                <span
                  className={`mt-3 inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    employee.disabled || employee.role.includes("(DISABLED)")
                      ? "bg-amber-50 text-amber-700"
                      : "bg-emerald-50 text-emerald-700"
                  }`}
                >
                  {employee.disabled || employee.role.includes("(DISABLED)") ? "Disabled" : "Active"}
                </span>
              </div>
            </div>
          </div>

          {/* Devices */}
          <div className="mt-6">
            <h2 className="text-lg font-semibold text-slate-800">Assigned Devices</h2>
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              {devices.length === 0 ? (
                <p className="col-span-full text-sm text-slate-400">No devices assigned.</p>
              ) : (
                devices.map((device) => (
                  <div
                    key={device.fleetHostId}
                    className="flex items-center gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
                  >
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-100 text-slate-500">
                      <Monitor className="h-5 w-5" />
                    </div>
                    <div>
                      <p className="font-medium text-slate-800">{device.hostname || device.fleetHostId}</p>
                      <p className="text-sm text-slate-500">
                        {device.osVersion || "Unknown OS"}
                      </p>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>

          {/* Deprovision */}
          <div className="mt-8 rounded-xl border border-red-200 bg-red-50 p-6">
            <div className="flex items-start gap-3">
              <AlertTriangle className="h-6 w-6 shrink-0 text-red-500" />
              <div className="flex-1">
                <h3 className="font-semibold text-red-800">Danger Zone</h3>
                <p className="mt-1 text-sm text-red-600">
                  This will permanently deprovision the employee account, revoke all sessions, and
                  remove access to all applications and devices. This action cannot be undone.
                </p>
                <button
                  onClick={() => setShowConfirm(true)}
                  disabled={deprovisioning}
                  className="mt-4 inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <AlertTriangle className="h-4 w-4" />
                  {deprovisioning ? "Deprovisioning..." : "Deprovision / Nuke Account"}
                </button>

                {actionError && (
                  <div className="mt-4 flex items-start gap-3 rounded-lg border border-red-300 bg-white px-4 py-3 text-sm text-red-700">
                    <AlertCircle className="mt-0.5 h-5 w-5 shrink-0" />
                    <div className="flex-1">
                      <p className="font-medium">Deprovisioning failed</p>
                      <p className="text-red-600">{actionError}</p>
                    </div>
                    <button
                      onClick={() => setActionError(null)}
                      className="text-red-400 hover:text-red-600"
                      aria-label="Dismiss"
                    >
                      ✕
                    </button>
                  </div>
                )}
              </div>
            </div>
          </div>
        </>
      )}

      <ConfirmDialog
        isOpen={showConfirm}
        onClose={() => setShowConfirm(false)}
        onConfirm={handleDeprovision}
        title="Deprovision Account?"
        message={`Are you sure you want to deprovision ${employee.firstName} ${employee.lastName}? This will immediately revoke all access.`}
        confirmLabel="Yes, Nuke Account"
        variant="danger"
      />
    </div>
  );
}
