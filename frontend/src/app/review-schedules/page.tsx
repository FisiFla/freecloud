"use client";

// E3 — Recurring access review schedules.
// Lists existing schedules, lets admins create new ones, toggle enabled, and delete.

import { useEffect, useState } from "react";
import { CalendarClock, Plus, Trash2, ToggleLeft, ToggleRight } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import LoadingRows from "@/components/LoadingRows";
import {
  listReviewSchedules,
  createReviewSchedule,
  updateReviewSchedule,
  deleteReviewSchedule,
  type ReviewSchedule,
} from "@/lib/api";
import { useApiReady } from "../providers";

const CADENCE_LABELS: Record<string, string> = {
  weekly: "Weekly",
  monthly: "Monthly",
  quarterly: "Quarterly",
};

export default function ReviewSchedulesPage() {
  const apiReady = useApiReady();
  const [schedules, setSchedules] = useState<ReviewSchedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // New schedule form
  const [showForm, setShowForm] = useState(false);
  const [newName, setNewName] = useState("");
  const [newCadence, setNewCadence] = useState<"weekly" | "monthly" | "quarterly">("monthly");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  // Per-item action state
  const [toggling, setToggling] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);

  const load = async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await listReviewSchedules();
      setSchedules(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load review schedules");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!apiReady) return;
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) return;
    setCreating(true);
    setCreateError(null);
    try {
      const created = await createReviewSchedule({ name: newName.trim(), cadence: newCadence });
      setSchedules((prev) => [created, ...prev]);
      setNewName("");
      setShowForm(false);
    } catch (e: unknown) {
      setCreateError(e instanceof Error ? e.message : "Failed to create schedule");
    } finally {
      setCreating(false);
    }
  };

  const handleToggleEnabled = async (schedule: ReviewSchedule) => {
    setToggling(schedule.id);
    try {
      const updated = await updateReviewSchedule(schedule.id, { enabled: !schedule.enabled });
      setSchedules((prev) => prev.map((s) => (s.id === updated.id ? updated : s)));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to update schedule");
    } finally {
      setToggling(null);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this review schedule? This cannot be undone.")) return;
    setDeleting(id);
    try {
      await deleteReviewSchedule(id);
      setSchedules((prev) => prev.filter((s) => s.id !== id));
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to delete schedule");
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="p-6 max-w-3xl space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-gray-900">Review Schedules</h1>
          <p className="mt-1 text-sm text-gray-500">
            Configure recurring access review campaigns to run automatically.
          </p>
        </div>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-3 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          <Plus className="h-4 w-4" />
          New Schedule
        </button>
      </div>

      {error && <ErrorBanner message={error} onDismiss={() => setError(null)} />}

      {/* New schedule form */}
      {showForm && (
        <form
          onSubmit={handleCreate}
          className="bg-white border border-gray-200 rounded-lg p-4 space-y-4"
        >
          <h2 className="text-sm font-semibold text-gray-700">New Review Schedule</h2>
          <div>
            <label htmlFor="scheduleName" className="block text-sm font-medium text-gray-700 mb-1">
              Name
            </label>
            <input
              id="scheduleName"
              type="text"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="Q1 Access Review"
              maxLength={200}
              className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label htmlFor="scheduleCadence" className="block text-sm font-medium text-gray-700 mb-1">
              Cadence
            </label>
            <select
              id="scheduleCadence"
              value={newCadence}
              onChange={(e) =>
                setNewCadence(e.target.value as "weekly" | "monthly" | "quarterly")
              }
              className="block w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
              <option value="quarterly">Quarterly</option>
            </select>
          </div>
          {createError && <p className="text-sm text-red-600">{createError}</p>}
          <div className="flex gap-3">
            <button
              type="submit"
              disabled={creating || !newName.trim()}
              className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:opacity-50"
            >
              {creating ? "Creating…" : "Create"}
            </button>
            <button
              type="button"
              onClick={() => {
                setShowForm(false);
                setCreateError(null);
              }}
              className="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {/* Schedules list */}
      {loading ? (
        <LoadingRows count={3} />
      ) : schedules.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-xl border-2 border-dashed border-gray-200 py-12 text-center">
          <CalendarClock className="h-8 w-8 text-gray-300" />
          <p className="text-sm text-gray-500">No review schedules yet.</p>
          <button
            onClick={() => setShowForm(true)}
            className="text-sm text-blue-600 hover:underline"
          >
            Create your first schedule
          </button>
        </div>
      ) : (
        <div className="space-y-3">
          {schedules.map((schedule) => (
            <div
              key={schedule.id}
              className="flex items-center justify-between rounded-lg border border-gray-200 bg-white px-4 py-3"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <p className="text-sm font-medium text-gray-900 truncate">{schedule.name}</p>
                  <span
                    className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                      schedule.enabled
                        ? "bg-green-100 text-green-700"
                        : "bg-gray-100 text-gray-500"
                    }`}
                  >
                    {schedule.enabled ? "Active" : "Paused"}
                  </span>
                  <span className="inline-flex items-center rounded-full bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-700">
                    {CADENCE_LABELS[schedule.cadence] ?? schedule.cadence}
                  </span>
                </div>
                <p className="mt-0.5 text-xs text-gray-500">
                  Next run: {new Date(schedule.nextRunAt).toLocaleDateString()}
                  {schedule.lastRunAt && (
                    <> · Last run: {new Date(schedule.lastRunAt).toLocaleDateString()}</>
                  )}
                </p>
              </div>
              <div className="ml-4 flex items-center gap-2 shrink-0">
                <button
                  onClick={() => handleToggleEnabled(schedule)}
                  disabled={toggling === schedule.id}
                  className="text-gray-400 hover:text-blue-600 disabled:opacity-50"
                  title={schedule.enabled ? "Pause schedule" : "Enable schedule"}
                >
                  {schedule.enabled ? (
                    <ToggleRight className="h-5 w-5 text-blue-600" />
                  ) : (
                    <ToggleLeft className="h-5 w-5" />
                  )}
                </button>
                <button
                  onClick={() => handleDelete(schedule.id)}
                  disabled={deleting === schedule.id}
                  className="text-gray-400 hover:text-red-500 disabled:opacity-50"
                  title="Delete schedule"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
