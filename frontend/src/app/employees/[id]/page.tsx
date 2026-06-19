"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Mail, Briefcase, Building2, Monitor, AlertTriangle, AlertCircle, CheckCircle, XCircle, Pencil, KeyRound, Lock, Package, ShieldCheck, ShieldAlert, ChevronRight } from "lucide-react";
import ConfirmDialog from "@/components/ConfirmDialog";
import { offboardUser, getUser, patchUser, resetPassword, getMFAStatus, requireMFA, lockDevice, listPolicies } from "@/lib/api";
import type { OffboardResponse, Device, PatchUserRequest, MFAStatus, Policy } from "@/lib/api";
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

  // A4 — edit form
  const [showEdit, setShowEdit] = useState(false);
  const [editFirst, setEditFirst] = useState("");
  const [editLast, setEditLast] = useState("");
  const [editDept, setEditDept] = useState("");
  const [editRole, setEditRole] = useState("");
  const [editEnabled, setEditEnabled] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveSuccess, setSaveSuccess] = useState(false);

  // A5 — password reset
  const [resetSending, setResetSending] = useState(false);
  const [resetMessage, setResetMessage] = useState<{ type: "success" | "error"; text: string } | null>(null);

  // C2: MFA state
  const [mfaStatus, setMfaStatus] = useState<MFAStatus | null>(null);
  const [mfaLoading, setMfaLoading] = useState(false);
  const [mfaError, setMfaError] = useState<string | null>(null);
  const [mfaSuccess, setMfaSuccess] = useState<string | null>(null);

  // B1: remote lock state
  const [lockingDeviceId, setLockingDeviceId] = useState<string | null>(null);
  const [lockConfirmDeviceId, setLockConfirmDeviceId] = useState<string | null>(null);
  const [lockSuccess, setLockSuccess] = useState<string | null>(null);
  const [lockError, setLockError] = useState<string | null>(null);

  // B4: policy assignment state
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [assigningPolicyForDeviceId, setAssigningPolicyForDeviceId] = useState<string | null>(null);
  const [selectedPolicyId, setSelectedPolicyId] = useState<string>("");
  const [policySuccess, setPolicySuccess] = useState<string | null>(null);
  const [policyError, setPolicyError] = useState<string | null>(null);

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
        setDevices(Array.isArray(userData.devices) ? userData.devices : []);

        // C2: fetch MFA status in parallel — non-blocking.
        getMFAStatus(userId).then(setMfaStatus).catch(() => {/* silently skip */});

        // Fetch policies for B4 (best-effort)
        try {
          const policyData = await listPolicies();
          setPolicies(Array.isArray(policyData.policies) ? policyData.policies : []);
        } catch {
          // Policy list is non-critical; don't block the page
        }
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load employee data");
      } finally {
        setLoading(false);
      }
    };
    fetchData();
  }, [userId, apiReady]);

  const openEdit = () => {
    if (!employee) return;
    setEditFirst(employee.firstName);
    setEditLast(employee.lastName);
    setEditDept(employee.department);
    setEditRole(employee.role);
    setEditEnabled(!employee.disabled);
    setSaveError(null);
    setSaveSuccess(false);
    setShowEdit(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!employee) return;
    setSaving(true);
    setSaveError(null);
    setSaveSuccess(false);
    const patch: PatchUserRequest = {};
    if (editFirst !== employee.firstName) patch.firstName = editFirst;
    if (editLast !== employee.lastName) patch.lastName = editLast;
    if (editDept !== employee.department) patch.department = editDept;
    if (editRole !== employee.role) patch.role = editRole;
    if (editEnabled === employee.disabled) patch.enabled = editEnabled;
    try {
      const updated = await patchUser(userId, patch);
      setEmployee({
        id: String(updated.id || ""),
        firstName: String(updated.firstName || ""),
        lastName: String(updated.lastName || ""),
        email: String(updated.email || employee.email),
        department: String(updated.department || ""),
        role: String(updated.role || ""),
        disabled: Boolean(updated.disabled),
      });
      setSaveSuccess(true);
      setTimeout(() => { setShowEdit(false); setSaveSuccess(false); }, 1200);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save");
    } finally {
      setSaving(false);
    }
  };

  const handlePasswordReset = async () => {
    setResetSending(true);
    setResetMessage(null);
    try {
      await resetPassword(userId);
      setResetMessage({ type: "success", text: "Password reset email sent." });
    } catch (err: unknown) {
      setResetMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to send reset email." });
    } finally {
      setResetSending(false);
    }
  };

  const handleRequireMFA = async (type: "totp" | "webauthn") => {
    setMfaLoading(true);
    setMfaError(null);
    setMfaSuccess(null);
    try {
      await requireMFA(userId, type);
      setMfaSuccess(`MFA requirement (${type.toUpperCase()}) set. User will be prompted on next login.`);
      const updated = await getMFAStatus(userId);
      setMfaStatus(updated);
    } catch (err: unknown) {
      setMfaError(err instanceof Error ? err.message : "Failed to set MFA requirement");
    } finally {
      setMfaLoading(false);
    }
  };

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

  // B1: lock device handler
  const handleLockDevice = async () => {
    if (!lockConfirmDeviceId) return;
    setLockingDeviceId(lockConfirmDeviceId);
    setLockError(null);
    setLockSuccess(null);
    try {
      await lockDevice(lockConfirmDeviceId);
      setLockSuccess(`Lock command sent to device ${lockConfirmDeviceId}.`);
    } catch (err: unknown) {
      setLockError(err instanceof Error ? err.message : "Failed to send lock command.");
    } finally {
      setLockingDeviceId(null);
      setLockConfirmDeviceId(null);
    }
  };

  // B2: Policy assignment is now team-scoped (see /teams page).
  // This handler shows a redirect hint instead of a direct assignment.
  const handleAssignPolicy = async () => {
    setPolicySuccess("Policy assignment is now managed via Fleet Teams. Use the Teams page to assign policies to teams and move devices there.");
    setAssigningPolicyForDeviceId(null);
    setSelectedPolicyId("");
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
                <div className="flex items-center justify-between">
                  <h1 className="text-xl font-bold text-slate-800">
                    {employee.firstName} {employee.lastName}
                  </h1>
                  <button
                    onClick={openEdit}
                    className="flex items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-600 hover:bg-slate-50"
                  >
                    <Pencil className="h-3.5 w-3.5" />
                    Edit
                  </button>
                </div>
                <div className="mt-3 space-y-2 text-sm text-slate-500">
                  <div className="flex items-center gap-2">
                    <Mail className="h-4 w-4" />
                    {employee.email}
                  </div>
                  <div className="flex items-center gap-2">
                    <Briefcase className="h-4 w-4" />
                    {employee.role || <span className="text-slate-300">No role set</span>}
                  </div>
                  <div className="flex items-center gap-2">
                    <Building2 className="h-4 w-4" />
                    {employee.department || <span className="text-slate-300">No department set</span>}
                  </div>
                </div>
                <div className="mt-3 flex items-center gap-3">
                  <span
                    className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                      employee.disabled || employee.role.includes("(DISABLED)")
                        ? "bg-amber-50 text-amber-700"
                        : "bg-emerald-50 text-emerald-700"
                    }`}
                  >
                    {employee.disabled || employee.role.includes("(DISABLED)") ? "Disabled" : "Active"}
                  </span>
                  {/* A5 — password reset */}
                  <button
                    onClick={handlePasswordReset}
                    disabled={resetSending || employee.disabled}
                    className="flex items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-1 text-xs font-medium text-slate-600 hover:bg-indigo-50 hover:text-indigo-700 hover:border-indigo-200 disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <KeyRound className="h-3.5 w-3.5" />
                    {resetSending ? "Sending..." : "Send Password Reset"}
                  </button>
                </div>
                {resetMessage && (
                  <p className={`mt-2 text-xs ${resetMessage.type === "success" ? "text-emerald-600" : "text-red-600"}`}>
                    {resetMessage.text}
                  </p>
                )}
              </div>
            </div>
          </div>

          {/* A4 — Inline edit form */}
          {showEdit && (
            <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
              <div className="fixed inset-0 bg-black/40" onClick={() => setShowEdit(false)} />
              <div className="relative w-full max-w-md rounded-xl bg-white p-6 shadow-2xl">
                <h3 className="text-lg font-semibold text-slate-800">Edit Employee</h3>
                <form onSubmit={handleSave} className="mt-4 space-y-4">
                  {saveError && (
                    <p className="text-sm text-red-600">{saveError}</p>
                  )}
                  {saveSuccess && (
                    <p className="text-sm text-emerald-600">Saved successfully!</p>
                  )}
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-xs font-medium text-slate-700">First Name</label>
                      <input
                        type="text"
                        value={editFirst}
                        onChange={(e) => setEditFirst(e.target.value)}
                        required
                        className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                      />
                    </div>
                    <div>
                      <label className="block text-xs font-medium text-slate-700">Last Name</label>
                      <input
                        type="text"
                        value={editLast}
                        onChange={(e) => setEditLast(e.target.value)}
                        required
                        className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                      />
                    </div>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-slate-700">Department</label>
                    <input
                      type="text"
                      value={editDept}
                      onChange={(e) => setEditDept(e.target.value)}
                      className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                    />
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-slate-700">Role</label>
                    <input
                      type="text"
                      value={editRole}
                      onChange={(e) => setEditRole(e.target.value)}
                      className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                    />
                  </div>
                  <div className="flex items-center gap-3">
                    <label className="text-sm font-medium text-slate-700">Account enabled</label>
                    <button
                      type="button"
                      onClick={() => setEditEnabled((v) => !v)}
                      className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${editEnabled ? "bg-indigo-600" : "bg-slate-300"}`}
                    >
                      <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${editEnabled ? "translate-x-6" : "translate-x-1"}`} />
                    </button>
                  </div>
                  <div className="flex justify-end gap-3 pt-2">
                    <button
                      type="button"
                      onClick={() => setShowEdit(false)}
                      className="rounded-lg border border-slate-200 px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-50"
                    >
                      Cancel
                    </button>
                    <button
                      type="submit"
                      disabled={saving}
                      className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
                    >
                      {saving ? "Saving..." : "Save Changes"}
                    </button>
                  </div>
                </form>
              </div>
            </div>
          )}

          {/* Quick links — B2/B3 device pages */}
          <div className="mt-4 flex gap-3">
            <a
              href={`/employees/${userId}/software`}
              className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm font-medium text-slate-600 shadow-sm transition-colors hover:bg-slate-50"
            >
              <Package className="h-4 w-4 text-indigo-500" />
              Software Inventory
              <ChevronRight className="h-4 w-4 text-slate-400" />
            </a>
            <a
              href={`/employees/${userId}/compliance`}
              className="flex items-center gap-2 rounded-lg border border-slate-200 bg-white px-4 py-2.5 text-sm font-medium text-slate-600 shadow-sm transition-colors hover:bg-slate-50"
            >
              <ShieldCheck className="h-4 w-4 text-indigo-500" />
              Compliance / Posture
              <ChevronRight className="h-4 w-4 text-slate-400" />
            </a>
          </div>

          {/* Lock / policy success / error banners */}
          {lockSuccess && (
            <div className="mt-4 flex items-center gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700">
              <CheckCircle className="h-4 w-4 shrink-0" />
              {lockSuccess}
              <button onClick={() => setLockSuccess(null)} className="ml-auto text-emerald-400 hover:text-emerald-600" aria-label="Dismiss">✕</button>
            </div>
          )}
          {lockError && (
            <div className="mt-4 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {lockError}
              <button onClick={() => setLockError(null)} className="ml-auto text-red-400 hover:text-red-600" aria-label="Dismiss">✕</button>
            </div>
          )}
          {policySuccess && (
            <div className="mt-4 flex items-center gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700">
              <CheckCircle className="h-4 w-4 shrink-0" />
              {policySuccess}
              <button onClick={() => setPolicySuccess(null)} className="ml-auto text-emerald-400 hover:text-emerald-600" aria-label="Dismiss">✕</button>
            </div>
          )}
          {policyError && (
            <div className="mt-4 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {policyError}
              <button onClick={() => setPolicyError(null)} className="ml-auto text-red-400 hover:text-red-600" aria-label="Dismiss">✕</button>
            </div>
          )}

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
                    className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
                  >
                    <div className="flex items-center gap-4">
                      <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-100 text-slate-500">
                        <Monitor className="h-5 w-5" />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="font-medium text-slate-800 truncate">{device.hostname || device.fleetHostId}</p>
                        <p className="text-sm text-slate-500">
                          {device.osVersion || "Unknown OS"}
                        </p>
                      </div>
                    </div>

                    {/* B1: Lock device button */}
                    <div className="mt-3 flex items-center gap-2 border-t border-slate-100 pt-3">
                      <button
                        onClick={() => setLockConfirmDeviceId(device.fleetHostId)}
                        disabled={lockingDeviceId === device.fleetHostId}
                        className="flex items-center gap-1.5 rounded-lg border border-amber-200 bg-amber-50 px-3 py-1.5 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-100 disabled:opacity-50"
                        title="Send a remote lock command to this device"
                      >
                        <Lock className="h-3.5 w-3.5" />
                        {lockingDeviceId === device.fleetHostId ? "Locking..." : "Lock Device"}
                      </button>

                      {/* B4: Assign policy — inline picker */}
                      {policies.length > 0 && (
                        <div className="flex items-center gap-1.5 flex-1">
                          <select
                            value={assigningPolicyForDeviceId === device.fleetHostId ? selectedPolicyId : ""}
                            onChange={(e) => {
                              setAssigningPolicyForDeviceId(device.fleetHostId);
                              setSelectedPolicyId(e.target.value);
                            }}
                            className="flex-1 min-w-0 rounded-lg border border-slate-200 bg-white px-2 py-1.5 text-xs text-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                          >
                            <option value="">Assign policy…</option>
                            {policies.map((p) => (
                              <option key={p.id} value={p.id}>{p.name}</option>
                            ))}
                          </select>
                          {assigningPolicyForDeviceId === device.fleetHostId && selectedPolicyId && (
                            <button
                              onClick={handleAssignPolicy}
                              className="rounded-lg bg-indigo-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-indigo-700"
                            >
                              Apply
                            </button>
                          )}
                        </div>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>

          {/* MFA Status — C2 */}
          <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="text-lg font-semibold text-slate-800">MFA Status</h2>
            {mfaStatus ? (
              <div className="mt-3 space-y-2">
                <div className="flex items-center gap-2 text-sm">
                  {mfaStatus.otpEnabled ? (
                    <ShieldCheck className="h-4 w-4 text-emerald-600" />
                  ) : (
                    <ShieldAlert className="h-4 w-4 text-slate-400" />
                  )}
                  <span className={mfaStatus.otpEnabled ? "text-emerald-700" : "text-slate-500"}>
                    TOTP / Authenticator app:{" "}
                    {mfaStatus.otpEnabled
                      ? "Enrolled"
                      : mfaStatus.otpPending
                      ? "Pending setup"
                      : "Not enrolled"}
                  </span>
                </div>
                <div className="flex items-center gap-2 text-sm">
                  {mfaStatus.webAuthnEnabled ? (
                    <ShieldCheck className="h-4 w-4 text-emerald-600" />
                  ) : (
                    <ShieldAlert className="h-4 w-4 text-slate-400" />
                  )}
                  <span className={mfaStatus.webAuthnEnabled ? "text-emerald-700" : "text-slate-500"}>
                    WebAuthn / Security key: {mfaStatus.webAuthnEnabled ? "Enrolled" : "Not enrolled"}
                  </span>
                </div>

                {mfaSuccess && (
                  <div className="mt-2 flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-700">
                    <CheckCircle className="h-4 w-4 shrink-0" />
                    {mfaSuccess}
                  </div>
                )}
                {mfaError && (
                  <div className="mt-2 flex items-center gap-2 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">
                    <AlertCircle className="h-4 w-4 shrink-0" />
                    {mfaError}
                  </div>
                )}

                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    onClick={() => handleRequireMFA("totp")}
                    disabled={mfaLoading || mfaStatus.otpEnabled}
                    className="rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {mfaLoading ? "Setting…" : "Require TOTP on next login"}
                  </button>
                  <button
                    onClick={() => handleRequireMFA("webauthn")}
                    disabled={mfaLoading || mfaStatus.webAuthnEnabled}
                    className="rounded-lg border border-slate-200 px-3 py-1.5 text-xs font-medium text-slate-700 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    {mfaLoading ? "Setting…" : "Require WebAuthn on next login"}
                  </button>
                </div>
              </div>
            ) : (
              <p className="mt-2 text-sm text-slate-400">Loading MFA status…</p>
            )}
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

      {/* Confirm deprovision */}
      <ConfirmDialog
        isOpen={showConfirm}
        onClose={() => setShowConfirm(false)}
        onConfirm={handleDeprovision}
        title="Deprovision Account?"
        message={`Are you sure you want to deprovision ${employee.firstName} ${employee.lastName}? This will immediately revoke all access.`}
        confirmLabel="Yes, Nuke Account"
        variant="danger"
      />

      {/* B1: Confirm lock */}
      <ConfirmDialog
        isOpen={lockConfirmDeviceId !== null}
        onClose={() => setLockConfirmDeviceId(null)}
        onConfirm={handleLockDevice}
        title="Lock Device?"
        message={`This will send a remote lock command to device ${lockConfirmDeviceId}. The device will be locked immediately. Unlike a wipe, data is preserved and the device can be unlocked later.`}
        confirmLabel="Yes, Lock Device"
        variant="default"
      />
    </div>
  );
}
