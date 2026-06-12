"use client";

import { useState } from "react";
import Link from "next/link";
import { Search, UserPlus, Trash2 } from "lucide-react";
import SlideOver from "@/components/SlideOver";
import OnboardForm from "./onboard/OnboardForm";

const mockEmployees = [
  {
    id: "1",
    firstName: "Alice",
    lastName: "Johnson",
    email: "alice@example.com",
    department: "Engineering",
    role: "Senior Developer",
    status: "Active",
  },
  {
    id: "2",
    firstName: "Bob",
    lastName: "Smith",
    email: "bob@example.com",
    department: "Marketing",
    role: "Marketing Lead",
    status: "Active",
  },
  {
    id: "3",
    firstName: "Carol",
    lastName: "Williams",
    email: "carol@example.com",
    department: "Operations",
    role: "Ops Manager",
    status: "Suspended",
  },
];

export default function EmployeesPage() {
  const [search, setSearch] = useState("");
  const [showOnboard, setShowOnboard] = useState(false);

  const filtered = mockEmployees.filter((e) => {
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

        {/* Table */}
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
                      title="Deprovision"
                      className="rounded-lg p-1.5 text-slate-400 transition-colors hover:bg-red-50 hover:text-red-600"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-sm text-slate-400">
                    No employees match your search.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Slide-over Onboard Panel */}
      <SlideOver isOpen={showOnboard} onClose={() => setShowOnboard(false)} title="Onboard New Employee">
        <OnboardForm onSuccess={() => setShowOnboard(false)} />
      </SlideOver>
    </>
  );
}
