"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Search, UserPlus, Trash2, AlertCircle, AlertTriangle, Users } from "lucide-react";
import SlideOver from "@/components/SlideOver";
import ConfirmDialog from "@/components/ConfirmDialog";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import LoadingRows from "@/components/LoadingRows";
import { Button, Input, Badge, Card } from "@/components/ui";
import OnboardForm from "./onboard/OnboardForm";
import { offboardUser, listUsers } from "@/lib/api";
import type { User } from "@/lib/api";
import { useApiReady } from "../providers";

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
  const apiReady = useApiReady();
  const [search, setSearch] = useState("");
  const [showOnboard, setShowOnboard] = useState(false);
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [formDirty, setFormDirty] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);

  const [deprovisionTarget, setDeprovisionTarget] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionWarnings, setActionWarnings] = useState<string[] | null>(null);

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
        status: u.disabled || String(u.role || "").includes("(DISABLED)") ? "Disabled" : "Active",
      }));
      setEmployees(mapped);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load employees");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!apiReady) return;
    fetchEmployees();
  }, [fetchEmployees, apiReady]);

  const handleDeprovision = async () => {
    if (!deprovisionTarget) return;
    setActionError(null);
    setActionWarnings(null);
    try {
      const result = await offboardUser(deprovisionTarget);
      if (result.warnings && result.warnings.length > 0) {
        setActionWarnings(result.warnings);
        fetchEmployees();
      } else {
        setEmployees((prev) => prev.filter((e) => e.id !== deprovisionTarget));
      }
    } catch (err: unknown) {
      setActionError(err instanceof Error ? err.message : "Failed to deprovision");
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
            <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Employees</h1>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">Manage users and their accounts.</p>
          </div>
          <Button onClick={() => setShowOnboard(true)}>
            <UserPlus className="h-4 w-4" />
            Onboard New Employee
          </Button>
        </div>

        {/* Error banner */}
        {error && (
          <div className="mt-4">
            <ErrorBanner message={error} onDismiss={() => setError(null)} />
          </div>
        )}

        {/* Deprovision action error */}
        {actionError && (
          <div className="mt-4 flex items-start gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
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

        {/* Deprovision partial-success warnings */}
        {actionWarnings && actionWarnings.length > 0 && (
          <div className="mt-4 flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
            <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-600" />
            <div className="flex-1">
              <p className="font-medium">Employee offboarded with warnings</p>
              <ul className="mt-1 list-inside list-disc text-amber-700 dark:text-amber-300">
                {actionWarnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
            <button
              onClick={() => setActionWarnings(null)}
              className="text-amber-400 hover:text-amber-600"
              aria-label="Dismiss"
            >
              ✕
            </button>
          </div>
        )}

        {/* Search */}
        <div className="relative mt-6">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <Input
            type="text"
            placeholder="Search employees..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-10"
          />
        </div>

        {/* Loading / Empty / Table */}
        {loading ? (
          <LoadingRows count={3} className="h-16" />
        ) : filtered.length === 0 && employees.length === 0 ? (
          <EmptyState
            icon={Users}
            title="No employees found"
            description="Onboard your first employee to get started."
          />
        ) : filtered.length === 0 && employees.length > 0 ? (
          <EmptyState
            icon={Search}
            title="No employees match your search"
            description="Try a different search term."
          />
        ) : (
          <Card padding={false} className="mt-6 overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-slate-100 bg-slate-50 text-left text-xs font-semibold uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-800 dark:text-slate-400">
                    <th className="px-6 py-3">Name</th>
                    <th className="px-6 py-3">Email</th>
                    <th className="px-6 py-3">Department</th>
                    <th className="px-6 py-3">Role</th>
                    <th className="px-6 py-3">Status</th>
                    <th className="px-6 py-3">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                  {filtered.map((emp) => (
                    <tr key={emp.id} className="transition-colors hover:bg-slate-50 dark:hover:bg-slate-800">
                      <td className="px-6 py-4">
                        <Link
                          href={`/employees/${emp.id}`}
                          className="font-medium text-indigo-600 hover:text-indigo-800 dark:text-indigo-400 dark:hover:text-indigo-300"
                        >
                          {emp.firstName} {emp.lastName}
                        </Link>
                      </td>
                      <td className="px-6 py-4 text-slate-600 dark:text-slate-400">{emp.email}</td>
                      <td className="px-6 py-4 text-slate-600 dark:text-slate-400">{emp.department}</td>
                      <td className="px-6 py-4 text-slate-600 dark:text-slate-400">{emp.role}</td>
                      <td className="px-6 py-4">
                        <Badge variant={emp.status === "Active" ? "success" : "warning"}>
                          {emp.status}
                        </Badge>
                      </td>
                      <td className="px-6 py-4">
                        <Button
                          variant="danger"
                          onClick={() => setDeprovisionTarget(emp.id)}
                          title="Deprovision"
                          className="p-2.5"
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        )}
      </div>

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

      <ConfirmDialog
        isOpen={confirmClose}
        onClose={() => setConfirmClose(false)}
        onConfirm={handleConfirmCloseYes}
        title="Discard changes?"
        message="The form has unsaved data. Are you sure you want to close?"
        confirmLabel="Yes, Discard"
        variant="default"
      />

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
