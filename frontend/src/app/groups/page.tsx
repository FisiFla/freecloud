"use client";

import { useEffect, useState } from "react";
import { Plus, Users, AlertCircle } from "lucide-react";
import EmptyState from "@/components/EmptyState";
import ErrorBanner from "@/components/ErrorBanner";
import { listGroups, createGroup } from "@/lib/api";
import type { Group } from "@/lib/api";
import { useApiReady } from "../providers";

export default function GroupsPage() {
  const apiReady = useApiReady();
  const [groups, setGroups] = useState<Group[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create form
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady) return;
    const fetchGroups = async () => {
      try {
        setLoading(true);
        setError(null);
        const data = await listGroups();
        setGroups(Array.isArray(data) ? data : []);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load groups");
      } finally {
        setLoading(false);
      }
    };
    fetchGroups();
  }, [apiReady]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      const created = await createGroup(newName.trim());
      setGroups((prev) => [...prev, created]);
      setShowCreate(false);
      setNewName("");
    } catch (err: unknown) {
      setCreateError(err instanceof Error ? err.message : "Failed to create group");
    } finally {
      setCreating(false);
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800">Groups</h1>
          <p className="mt-1 text-sm text-slate-500">Manage Keycloak realm groups.</p>
        </div>
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
        >
          <Plus className="h-4 w-4" />
          New Group
        </button>
      </div>

      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} onDismiss={() => setError(null)} />
        </div>
      )}

      {/* Inline create form */}
      {showCreate && (
        <form onSubmit={handleCreate} className="mt-4 flex items-end gap-3 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
          <div className="flex-1">
            <label className="block text-sm font-medium text-slate-700">Group Name</label>
            <input
              type="text"
              required
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="e.g. Engineering"
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            />
            {createError && (
              <p className="mt-1 flex items-center gap-1 text-xs text-red-600">
                <AlertCircle className="h-3.5 w-3.5" />
                {createError}
              </p>
            )}
          </div>
          <button
            type="submit"
            disabled={creating || !newName.trim()}
            className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {creating ? "Creating..." : "Create"}
          </button>
          <button
            type="button"
            onClick={() => { setShowCreate(false); setNewName(""); setCreateError(null); }}
            className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-50"
          >
            Cancel
          </button>
        </form>
      )}

      {loading ? (
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-16 animate-pulse rounded-xl bg-slate-200" />
          ))}
        </div>
      ) : (
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {groups.map((g) => (
            <div
              key={g.id}
              className="flex items-center gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
            >
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                <Users className="h-5 w-5" />
              </div>
              <div className="min-w-0">
                <p className="font-medium text-slate-800 truncate">{g.name}</p>
                <p className="text-xs text-slate-400 font-mono truncate">{g.id}</p>
              </div>
            </div>
          ))}
          {groups.length === 0 && !loading && (
            <div className="col-span-full">
              <EmptyState
                icon={Users}
                title="No groups yet"
                description="Create your first group to organise users."
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
