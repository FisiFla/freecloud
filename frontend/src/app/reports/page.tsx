"use client";

import { useState } from "react";
import { Download, FileBarChart2 } from "lucide-react";
import { downloadReport } from "@/lib/api";

type ReportType = "compliance" | "access-review";
type ReportFormat = "csv" | "json";

interface ReportConfig {
  type: ReportType;
  label: string;
  description: string;
}

const REPORTS: ReportConfig[] = [
  {
    type: "compliance",
    label: "Compliance Posture",
    description:
      "Device-level security posture: disk encryption, firewall, MDM enrollment, vulnerabilities, and compliance status across all enrolled devices.",
  },
  {
    type: "access-review",
    label: "Access Review Status",
    description:
      "Summary of all access review campaigns: total items, confirmed, revoked, and pending decisions per campaign.",
  },
];

export default function ReportsPage() {
  const [format, setFormat] = useState<ReportFormat>("csv");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");

  // Convert date-only "yyyy-MM-dd" to RFC3339 for the backend.
  const fromRFC3339 = dateFrom ? `${dateFrom}T00:00:00Z` : undefined;
  const toRFC3339 = dateTo ? `${dateTo}T23:59:59Z` : undefined;

  return (
    <div>
      <div>
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Reports</h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          Download on-demand compliance and access-review reports.
        </p>
      </div>

      {/* Global options */}
      <div className="mt-6 flex flex-wrap items-end gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900">
        <div>
          <label htmlFor="report-format" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            Format
          </label>
          <select
            id="report-format"
            value={format}
            onChange={(e) => setFormat(e.target.value as ReportFormat)}
            className="rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
          >
            <option value="csv">CSV</option>
            <option value="json">JSON</option>
          </select>
        </div>

        <div>
          <label htmlFor="report-from" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            From
          </label>
          <input
            id="report-from"
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
          />
        </div>

        <div>
          <label htmlFor="report-to" className="block text-xs font-medium uppercase tracking-wider text-slate-500 mb-1 dark:text-slate-400">
            To
          </label>
          <input
            id="report-to"
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="rounded-lg border border-slate-200 bg-white py-2 px-3 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
          />
        </div>
      </div>

      {/* Report cards */}
      <div className="mt-6 grid gap-4 sm:grid-cols-2">
        {REPORTS.map((report) => (
          <div
            key={report.type}
            className="flex flex-col gap-4 rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <div className="flex items-start gap-3">
              <div className="rounded-lg bg-indigo-50 p-2 dark:bg-indigo-950">
                <FileBarChart2 className="h-5 w-5 text-indigo-600 dark:text-indigo-400" />
              </div>
              <div>
                <h2 className="text-sm font-semibold text-slate-800 dark:text-slate-100">
                  {report.label}
                </h2>
                <p className="mt-1 text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
                  {report.description}
                </p>
              </div>
            </div>

            <button
              onClick={() =>
                downloadReport(report.type, format, {
                  from: fromRFC3339,
                  to: toRFC3339,
                })
              }
              className="flex items-center justify-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-xs font-medium text-white transition-colors hover:bg-indigo-700 dark:bg-indigo-700 dark:hover:bg-indigo-600"
            >
              <Download className="h-3.5 w-3.5" />
              Download {format.toUpperCase()}
            </button>
          </div>
        ))}
      </div>

      <p className="mt-6 text-xs text-slate-400 dark:text-slate-500">
        Reports are generated on demand and reflect the current state of your FreeCloud instance.
        Date range applies only to access-review reports (campaign creation date).
        Compliance reports always reflect the live device posture.
      </p>
    </div>
  );
}
