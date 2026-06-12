"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import { Mail, Briefcase, Building2, Monitor, AlertTriangle } from "lucide-react";
import ConfirmDialog from "@/components/ConfirmDialog";

// Mock employee data
const mockEmployee = {
  id: "1",
  firstName: "Alice",
  lastName: "Johnson",
  email: "alice@example.com",
  department: "Engineering",
  role: "Senior Developer",
  status: "Active",
  devices: [
    { id: "d1", name: "MacBook Pro 16&quot;", type: "Laptop", os: "macOS 14.3" },
    { id: "d2", name: "iPhone 15 Pro", type: "Phone", os: "iOS 17.4" },
  ],
};

export default function EmployeeDetailPage() {
  const params = useParams();
  const [showConfirm, setShowConfirm] = useState(false);
  const [deprovisioning, setDeprovisioning] = useState(false);
  const [deprovisioned, setDeprovisioned] = useState(false);

  const employee = mockEmployee; // In real app, fetch from GET /api/v1/users/{id}

  const handleDeprovision = async () => {
    setDeprovisioning(true);
    try {
      await fetch(`http://localhost:8080/api/v1/offboard/${params.id}`, {
        method: "POST",
      });
      setDeprovisioned(true);
    } catch {
      alert("Failed to deprovision. Check the backend is running.");
    } finally {
      setDeprovisioning(false);
    }
  };

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
        <div className="mt-8 rounded-xl border border-red-200 bg-red-50 p-8 text-center">
          <AlertTriangle className="mx-auto h-10 w-10 text-red-500" />
          <h2 className="mt-3 text-lg font-semibold text-red-800">Account Deprovisioned</h2>
          <p className="mt-1 text-sm text-red-600">
            {employee.firstName} {employee.lastName} has been deprovisioned.
          </p>
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
                    employee.status === "Active"
                      ? "bg-emerald-50 text-emerald-700"
                      : "bg-amber-50 text-amber-700"
                  }`}
                >
                  {employee.status}
                </span>
              </div>
            </div>
          </div>

          {/* Devices */}
          <div className="mt-6">
            <h2 className="text-lg font-semibold text-slate-800">Assigned Devices</h2>
            <div className="mt-3 grid gap-4 sm:grid-cols-2">
              {employee.devices.map((device) => (
                <div
                  key={device.id}
                  className="flex items-center gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
                >
                  <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-slate-100 text-slate-500">
                    <Monitor className="h-5 w-5" />
                  </div>
                  <div>
                    <p className="font-medium text-slate-800">{device.name}</p>
                    <p className="text-sm text-slate-500">
                      {device.type} &middot; {device.os}
                    </p>
                  </div>
                </div>
              ))}
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
