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
  pending:  "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100  text-green-800",
  rejected: "bg-red-100    text-red-800",
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
          <h1 className="text-2xl font-bold text-gray-900">Approval Requests</h1>
          <p className="mt-1 text-sm text-gray-500">
            Privileged actions (onboard / offboard) submitted by helpdesk, pending super-admin sign-off.
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => setShowSubmit((v) => !v)}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-white border rounded-md hover:bg-gray-50"
          >
            + Submit Request
          </button>
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-white border rounded-md hover:bg-gray-50 disabled:opacity-50"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} />
            Refresh
          </button>
        </div>
      </div>

      {showSubmit && (
        <div className="border rounded-lg p-4 bg-gray-50">
          <h2 className="text-sm font-semibold text-gray-700 mb-3">Submit a New Request</h2>
          <form onSubmit={handleSubmit} className="space-y-3">
            <div className="flex gap-3">
              <div>
                <label className="block text-xs text-gray-500 mb-1">Action Type</label>
                <select
                  value={submitActionType}
                  onChange={(e) => setSubmitActionType(e.target.value as "onboard" | "offboard")}
                  className="border rounded px-2 py-1.5 text-sm"
                >
                  <option value="onboard">Onboard</option>
                  <option value="offboard">Offboard</option>
                </select>
              </div>
              <div className="flex-1">
                <label className="block text-xs text-gray-500 mb-1">
                  Payload (JSON) — e.g. <code className="bg-gray-200 px-1 rounded">{"{\"email\":\"user@example.com\",\"firstName\":\"Jo\",\"lastName\":\"Doe\"}"}</code>
                </label>
                <textarea
                  value={submitPayload}
                  onChange={(e) => setSubmitPayload(e.target.value)}
                  className="w-full border rounded px-2 py-1.5 text-sm font-mono"
                  rows={2}
                  placeholder='{"email":"user@example.com","firstName":"Jo","lastName":"Doe"}'
                />
              </div>
            </div>
            {submitError && <p className="text-xs text-red-600">{submitError}</p>}
            {submitMsg  && <p className="text-xs text-green-700">{submitMsg}</p>}
            <button
              type="submit"
              disabled={submitting || !submitPayload.trim()}
              className="px-3 py-1.5 text-sm bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
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
                ? "bg-gray-900 text-white border-gray-900"
                : "bg-white text-gray-600 hover:bg-gray-50"
            }`}
          >
            {s}
          </button>
        ))}
      </div>

      <div className="bg-white border rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
            <tr>
              <th className="px-4 py-3">Type</th>
              <th className="px-4 py-3">Requester</th>
              <th className="px-4 py-3">Payload</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Submitted</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
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
                <tr key={req.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-medium capitalize">{req.actionType}</td>
                  <td className="px-4 py-3 text-gray-600">{req.requesterId}</td>
                  <td className="px-4 py-3 max-w-xs truncate font-mono text-xs text-gray-500">
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
                  <td className="px-4 py-3 text-gray-500">
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
                      <span className="text-xs text-gray-400">
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
