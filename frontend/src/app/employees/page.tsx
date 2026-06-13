"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Search, UserPlus, Trash2, AlertCircle, AlertTriangle, Users } from "lucide-react";
import SlideOver from "@/components/SlideOver";
import ConfirmDialog from "@/components/ConfirmDialog";
import OnboardForm from "./onboard/OnboardForm";
import { offboardUser, listUsers } from "@/lib/api";
import type { User } from "@/lib/api";

interface Employee {
  id: string;
  firstName: string;
  lastName: string;
  email: string;
  department: string;
  role: string;
  status: string;
}

export default function EmployeesPage() {
  const [search, setSearch] = useState("");
  const [showOnboard, setShowOnboard] = useState(false);
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [formDirty, setFormDirty] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);

  const [deprovisionTarget, setDeprovisionTarget] = useState<string | null>(null);

  const fetchEmployees = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await listUsers();
      const mapped = (Array.isArray(data) ? data : []).map((u: User) => ({
        id: String(u.keycloakUserId || ""),
        firstName: String(u.firstName || ""),
        lastName: String(u.lastName || ""),
        email: String(u.email || ""),
        department: String(u.department || ""),
        role: String(u.role || ""),
        status: String(u.status || "Active"),
      }));
      setEmployees(mapped);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load employees");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchEmployees();
  }, [fetchEmployees]);

  const handleDeprovision = async () => {
    if (!deprovisionTarget) return;
    try {
      await offboardUser(deprovisionTarget);
      setEmployees((prev) => prev.filter((e) => e.id !== deprovisionTarget));
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Failed to deprovision");
    } finally {
      setDeprovisionTarget(null);
    }
  };

  const handleSlideOverClose = () => {
    if (formDirty) {
      setConfirmClose(true);
      return false;
    }
    return true;
  };

  const handleConfirmCloseYes = () => {
    setConfirmClose(false);
    setFormDirty(false);
    setShowOnboard(false);
  };

  const filtered = employees.filter((e) => {
    const q = search.toLowerCase();
    return (
      e.firstName.toLowerCase().includes(q) ||
      e.lastName.toLowerCase().includes(q) ||
      e.email.toLowerCase().includes(q) ||
      e.department.toLowerCase().includes(q)
    );
  });

  return (
    <>
      <div>
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-slate-800">Employees</h1>
            <p className="mt-1 text-sm text-slate-500">Manage users and their accounts.</p>
          </div>
          <button
            onClick={() => setShowOnboard(true)}
            className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
          >
            <UserPlus className="h-4 w-4" />
            Onboard New Employee
          </button>
        </div>

        {/* Error banner */}
        {error && (
          <div className="mt-4 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
            <AlertCircle className="h-5 w-5 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {/* Search */}
        <div className="mt-6 relative">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <input
            type="text"
            placeholder="Search employees..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-slate-200 bg-white py-2.5 pl-10 pr-4 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          />
        </div>

        {/* Loading skeleton */}
        {loading ? (
          <div className="mt-6 space-y-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-16 animate-pulse rounded-xl bg-slate-200" />
            ))}
          </div>
        ) : filtered.length === 0 && employees.length === 0 ? (
          <div className="mt-8 text-center rounded-xl border border-dashed border-slate-200 bg-white p-12">
            <Users className="mx-auto h-8 w-8 text-slate-300" />
            <h3 className="mt-3 text-sm font-medium text-slate-600">No employees found</h3>
            <p className="mt-1 text-sm text-slate-400">Onboard your first employee to get started.</p>
          </div>
        ) : filtered.length === 0 && employees.length > 0 ? (
          <div className="mt-8 text-center rounded-xl border border-dashed border-slate-200 bg-white p-12">
            <Search className="mx-auto h-8 w-8 text-slate-300" />
            <h3 className="mt-3 text-sm font-medium text-slate-600">No employees match your search</h3>
            <p className="mt-1 text-sm text-slate-400">Try a different search term.</p>
          </div>
        ) : (
          /* Table */
          <div className="mt-6 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500">
                  <th className="px-6 py-3">Name</th>
                  <th className="px-6 py-3">Email</th>
                  <th className="px-6 py-3">Department</th>
                  <th className="px-6 py-3">Role</th>
                  <th className="px-6 py-3">Status</th>
                  <th className="px-6 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {filtered.map((emp) => (
                  <tr key={emp.id} className="hover:bg-slate-50 transition-colors">
                    <td className="px-6 py-4">
                      <Link
                        href={`/employees/${emp.id}`}
                        className="font-medium text-indigo-600 hover:text-indigo-800"
                      >
                        {emp.firstName} {emp.lastName}
                      </Link>
                    </td>
                    <td className="px-6 py-4 text-slate-600">{emp.email}</td>
                    <td className="px-6 py-4 text-slate-600">{emp.department}</td>
                    <td className="px-6 py-4 text-slate-600">{emp.role}</td>
                    <td className="px-6 py-4">
                      <span
                        className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                          emp.status === "Active"
                            ? "bg-emerald-50 text-emerald-700"
                            : "bg-amber-50 text-amber-700"
                        }`}
                      >
                        {emp.status}
                      </span>
                    </td>
                    <td className="px-6 py-4">
                      <button
                        onClick={() => setDeprovisionTarget(emp.id)}
                        title="Deprovision"
                        className="rounded-lg p-1.5 text-slate-400 transition-colors hover:bg-red-50 hover:text-red-600"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Slide-over Onboard Panel */}
      <SlideOver
        isOpen={showOnboard}
        onClose={() => setShowOnboard(false)}
        title="Onboard New Employee"
        beforeClose={handleSlideOverClose}
      >
        <OnboardForm
          onSuccess={() => { setShowOnboard(false); fetchEmployees(); }}
          onDirtyChange={setFormDirty}
        />
      </SlideOver>

      {/* Confirm close dialog (dirty form) */}
      <ConfirmDialog
        isOpen={confirmClose}
        onClose={() => setConfirmClose(false)}
        onConfirm={handleConfirmCloseYes}
        title="Discard changes?"
        message="The form has unsaved data. Are you sure you want to close?"
        confirmLabel="Yes, Discard"
        variant="default"
      />

      {/* Confirm deprovision dialog */}
      <ConfirmDialog
        isOpen={deprovisionTarget !== null}
        onClose={() => setDeprovisionTarget(null)}
        onConfirm={handleDeprovision}
        title="Deprovision Employee?"
        message="This will permanently deprovision this employee. This action cannot be undone."
        confirmLabel="Yes, Deprovision"
        variant="danger"
      />
    </>
  );
}
