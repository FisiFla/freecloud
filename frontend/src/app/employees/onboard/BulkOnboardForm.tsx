"use client";

import { useRef, useState } from "react";
import { Upload, CheckCircle, XCircle, AlertCircle, SkipForward } from "lucide-react";
import { bulkOnboardEmployees, ApiError } from "@/lib/api";
import type { BulkOnboardResponse, BulkOnboardRowResult } from "@/lib/api";

interface BulkOnboardFormProps {
  onSuccess?: () => void;
}

const statusIcon = (status: BulkOnboardRowResult["status"]) => {
  if (status === "succeeded") return <CheckCircle className="h-4 w-4 text-emerald-500" />;
  if (status === "skipped-duplicate") return <SkipForward className="h-4 w-4 text-amber-500" />;
  return <XCircle className="h-4 w-4 text-red-500" />;
};

export default function BulkOnboardForm({ onSuccess }: BulkOnboardFormProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<BulkOnboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0] ?? null;
    setFile(f);
    setResult(null);
    setError(null);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!file) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const data = await bulkOnboardEmployees(file);
      setResult(data);
      if (data.succeeded > 0) onSuccess?.();
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.message : "Upload failed. Please check the file and try again.");
    } finally {
      setSubmitting(false);
    }
  };

  const csvTemplate =
    "firstName,lastName,email,department,role\nJane,Doe,jane@example.com,Engineering,Software Engineer\n";

  const downloadTemplate = () => {
    const blob = new Blob([csvTemplate], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "bulk-onboard-template.csv";
    a.click();
    URL.revokeObjectURL(url);
  };

  if (result) {
    return (
      <div className="space-y-4">
        {/* Summary bar */}
        <div className="grid grid-cols-3 gap-3">
          <div className="rounded-lg bg-emerald-50 p-3 text-center">
            <p className="text-2xl font-bold text-emerald-700">{result.succeeded}</p>
            <p className="text-xs text-emerald-600">Succeeded</p>
          </div>
          <div className="rounded-lg bg-amber-50 p-3 text-center">
            <p className="text-2xl font-bold text-amber-700">{result.skipped}</p>
            <p className="text-xs text-amber-600">Skipped</p>
          </div>
          <div className="rounded-lg bg-red-50 p-3 text-center">
            <p className="text-2xl font-bold text-red-700">{result.failed}</p>
            <p className="text-xs text-red-600">Failed</p>
          </div>
        </div>

        {/* Per-row results */}
        <div className="max-h-64 overflow-y-auto rounded-xl border border-slate-200 bg-white">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-slate-50">
              <tr className="border-b border-slate-100 text-xs font-semibold uppercase tracking-wider text-slate-500">
                <th className="px-4 py-2 text-left">Row</th>
                <th className="px-4 py-2 text-left">Email</th>
                <th className="px-4 py-2 text-left">Status</th>
                <th className="px-4 py-2 text-left">Note</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {result.results.map((row) => (
                <tr key={row.row} className="hover:bg-slate-50">
                  <td className="px-4 py-2 text-slate-500">{row.row}</td>
                  <td className="px-4 py-2 text-slate-700">{row.email}</td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-1.5">
                      {statusIcon(row.status)}
                      <span className="text-xs capitalize">{row.status.replace("-", " ")}</span>
                    </span>
                  </td>
                  <td className="px-4 py-2 text-xs text-slate-400">{row.reason ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <button
          type="button"
          onClick={() => {
            setResult(null);
            setFile(null);
            if (inputRef.current) inputRef.current.value = "";
          }}
          className="w-full rounded-lg bg-slate-100 px-4 py-2.5 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-200"
        >
          Upload Another File
        </button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      {/* Template download */}
      <div className="rounded-lg bg-indigo-50 p-3 text-sm text-indigo-700">
        <span className="font-medium">CSV format:</span> firstName, lastName, email, department, role.{" "}
        <button
          type="button"
          onClick={downloadTemplate}
          className="underline hover:text-indigo-900"
        >
          Download template
        </button>
      </div>

      {/* File input */}
      <div>
        <label className="block text-sm font-medium text-slate-700">CSV File</label>
        <div
          className="mt-1 flex cursor-pointer flex-col items-center gap-2 rounded-xl border-2 border-dashed border-slate-300 bg-slate-50 px-6 py-8 text-center transition-colors hover:border-indigo-400 hover:bg-indigo-50"
          onClick={() => inputRef.current?.click()}
        >
          <Upload className="h-8 w-8 text-slate-400" />
          {file ? (
            <p className="text-sm font-medium text-slate-700">{file.name}</p>
          ) : (
            <p className="text-sm text-slate-500">
              Click to select a CSV file, or drag and drop
            </p>
          )}
          <input
            ref={inputRef}
            type="file"
            accept=".csv,text/csv"
            onChange={handleFileChange}
            className="hidden"
          />
        </div>
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-lg bg-red-50 p-3 text-sm text-red-700">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </div>
      )}

      <button
        type="submit"
        disabled={!file || submitting}
        className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {submitting ? "Uploading…" : "Upload & Onboard"}
      </button>
    </form>
  );
}
