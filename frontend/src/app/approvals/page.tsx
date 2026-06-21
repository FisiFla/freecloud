"use client";

import { useEffect, useState } from "react";
import { CheckCircle, XCircle, Clock, RefreshCw, Inbox } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import LoadingRows from "@/components/LoadingRows";
import {
  listApprovalRequests,
  decideApprovalRequest,
  submitApprovalRequest,
} from "@/lib/api";
import type { ApprovalRequestItem } from "@/lib/api";
import { useApiReady } from "../providers";

const statusBadge: Record<string, string> = {
  pending:  "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-300",
  approved: "bg-green-100  text-green-800  dark:bg-emerald-900/40 dark:text-emerald-300",
  rejected: "bg-red-100    text-red-800    dark:bg-red-900/40 dark:text-red-300",
};

export default function ApprovalsPage() {
  const apiReady = useApiReady();
  const [requests, setRequests] = useState<ApprovalRequestItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState<string | null>(null);
  const [filter, setFilter] = useState<"pending" | "approved" | "rejected" | "all">("pending");

  // Submit a new request (helpdesk view)
  const [showSubmit, setShowSubmit] = useState(false);
  const [submitActionType, setSubmitActionType] = useState<"onboard" | "offboard">("onboard");
  const [submitPayload, setSubmitPayload] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitMsg, setSubmitMsg] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const load = async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await listApprovalRequests(filter);
      setRequests(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load approval requests");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady, filter]);

  const handleDecide = async (id: string, decision: "approved" | "rejected") => {
    try {
      setDeciding(id);
      await decideApprovalRequest(id, decision);
      await load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Decision failed");
    } finally {
      setDeciding(null);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitMsg(null);
    setSubmitError(null);
    let payload: Record<string, unknown>;
    try {
      payload = JSON.parse(submitPayload);
    } catch {
      setSubmitError("Payload must be valid JSON");
      return;
    }
    try {
      setSubmitting(true);
      await submitApprovalRequest(submitActionType, payload);
      setSubmitMsg("Request submitted — pending admin approval.");
      setSubmitPayload("");
    } catch (e: unknown) {
      setSubmitError(e instanceof Error ? e.message : "Submit failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Approval Requests</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Privileged actions (onboard / offboard) submitted by helpdesk, pending super-admin sign-off.
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => setShowSubmit((v) => !v)}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-white border border-slate-200 rounded-md hover:bg-slate-50 text-slate-600 dark:bg-slate-800 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-700"
          >
            + Submit Request
          </button>
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-white border border-slate-200 rounded-md hover:bg-slate-50 text-slate-600 disabled:opacity-50 dark:bg-slate-800 dark:border-slate-700 dark:text-slate-300 dark:hover:bg-slate-700"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} />
            Refresh
          </button>
        </div>
      </div>

      {showSubmit && (
        <div className="border border-slate-200 rounded-lg p-4 bg-slate-50 dark:border-slate-700 dark:bg-slate-800">
          <h2 className="text-sm font-semibold text-slate-700 mb-3 dark:text-slate-300">Submit a New Request</h2>
          <form onSubmit={handleSubmit} className="space-y-3">
            <div className="flex gap-3">
              <div>
                <label className="block text-xs text-slate-500 mb-1 dark:text-slate-400">Action Type</label>
                <select
                  value={submitActionType}
                  onChange={(e) => setSubmitActionType(e.target.value as "onboard" | "offboard")}
                  className="border border-slate-200 rounded px-2 py-1.5 text-sm text-slate-700 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-100"
                >
                  <option value="onboard">Onboard</option>
                  <option value="offboard">Offboard</option>
                </select>
              </div>
              <div className="flex-1">
                <label className="block text-xs text-slate-500 mb-1 dark:text-slate-400">
                  Payload (JSON) — e.g. <code className="bg-slate-200 px-1 rounded dark:bg-slate-600 dark:text-slate-200">{"{\"email\":\"user@example.com\",\"firstName\":\"Jo\",\"lastName\":\"Doe\"}"}</code>
                </label>
                <textarea
                  value={submitPayload}
                  onChange={(e) => setSubmitPayload(e.target.value)}
                  className="w-full border border-slate-200 rounded px-2 py-1.5 text-sm font-mono text-slate-700 dark:border-slate-600 dark:bg-slate-700 dark:text-slate-100"
                  rows={2}
                  placeholder='{"email":"user@example.com","firstName":"Jo","lastName":"Doe"}'
                />
              </div>
            </div>
            {submitError && <p className="text-xs text-red-600 dark:text-red-400">{submitError}</p>}
            {submitMsg  && <p className="text-xs text-emerald-700 dark:text-emerald-400">{submitMsg}</p>}
            <button
              type="submit"
              disabled={submitting || !submitPayload.trim()}
              className="px-3 py-1.5 text-sm bg-indigo-600 text-white rounded hover:bg-indigo-700 disabled:opacity-50 dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {submitting ? "Submitting…" : "Submit"}
            </button>
          </form>
        </div>
      )}

      {error && <ErrorBanner message={error} onDismiss={() => setError(null)} />}

      <div className="flex gap-2 text-sm">
        {(["pending", "approved", "rejected", "all"] as const).map((s) => (
          <button
            key={s}
            onClick={() => setFilter(s)}
            className={`px-3 py-1 rounded-full border capitalize ${
              filter === s
                ? "bg-slate-900 text-white border-slate-900 dark:bg-slate-100 dark:text-slate-900 dark:border-slate-100"
                : "bg-white text-slate-600 border-slate-200 hover:bg-slate-50 dark:bg-slate-800 dark:text-slate-400 dark:border-slate-700 dark:hover:bg-slate-700"
            }`}
          >
            {s}
          </button>
        ))}
      </div>

      <div className="bg-white border border-slate-200 rounded-lg overflow-hidden dark:bg-slate-900 dark:border-slate-700">
        <table className="w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs font-medium text-slate-500 uppercase tracking-wider dark:bg-slate-800 dark:text-slate-400">
            <tr>
              <th className="px-4 py-3">Type</th>
              <th className="px-4 py-3">Requester</th>
              <th className="px-4 py-3">Payload</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Submitted</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-700">
            {loading ? (
              <tr>
                <td colSpan={6}><LoadingRows count={5} /></td>
              </tr>
            ) : requests.length === 0 ? (
              <tr>
                <td colSpan={6}>
                  <EmptyState icon={Inbox} title="No approval requests found." />
                </td>
              </tr>
            ) : (
              requests.map((req) => (
                <tr key={req.id} className="hover:bg-slate-50 dark:hover:bg-slate-800/50">
                  <td className="px-4 py-3 font-medium capitalize text-slate-800 dark:text-slate-100">{req.actionType}</td>
                  <td className="px-4 py-3 text-slate-600 dark:text-slate-400">{req.requesterId}</td>
                  <td className="px-4 py-3 max-w-xs truncate font-mono text-xs text-slate-500 dark:text-slate-400">
                    {JSON.stringify(req.payload)}
                  </td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${statusBadge[req.status] ?? ""}`}>
                      {req.status === "pending"  && <Clock className="w-3 h-3" />}
                      {req.status === "approved" && <CheckCircle className="w-3 h-3" />}
                      {req.status === "rejected" && <XCircle className="w-3 h-3" />}
                      {req.status}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-slate-500 dark:text-slate-400">
                    {req.createdAt ? new Date(req.createdAt).toLocaleString() : "—"}
                  </td>
                  <td className="px-4 py-3">
                    {req.status === "pending" && (
                      <div className="flex gap-2">
                        <button
                          onClick={() => handleDecide(req.id, "approved")}
                          disabled={deciding === req.id}
                          className="text-xs px-2 py-1 bg-green-600 text-white rounded hover:bg-green-700 disabled:opacity-50"
                        >
                          Approve
                        </button>
                        <button
                          onClick={() => handleDecide(req.id, "rejected")}
                          disabled={deciding === req.id}
                          className="text-xs px-2 py-1 bg-red-600 text-white rounded hover:bg-red-700 disabled:opacity-50"
                        >
                          Reject
                        </button>
                      </div>
                    )}
                    {req.status !== "pending" && (
                      <span className="text-xs text-slate-400 dark:text-slate-500">
                        {req.decidedBy ? `by ${req.decidedBy}` : "—"}
                      </span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
