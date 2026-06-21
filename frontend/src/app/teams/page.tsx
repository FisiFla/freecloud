"use client";

import { useEffect, useState } from "react";
import { Plus, Shield, AlertCircle } from "lucide-react";
import EmptyState from "@/components/EmptyState";
import ErrorBanner from "@/components/ErrorBanner";
import { listTeams, createTeam, listPolicies, assignPolicyToTeam } from "@/lib/api";
import type { FleetTeam, Policy } from "@/lib/api";
import { useApiReady } from "../providers";

export default function TeamsPage() {
  const apiReady = useApiReady();
  const [teams, setTeams] = useState<FleetTeam[]>([]);
  const [policies, setPolicies] = useState<Policy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create form
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState("");
  const [newDesc, setNewDesc] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  // Policy assignment
  const [assignTeamID, setAssignTeamID] = useState<number | null>(null);
  const [assignPolicyID, setAssignPolicyID] = useState("");
  const [assigning, setAssigning] = useState(false);
  const [assignError, setAssignError] = useState<string | null>(null);
  const [assignOK, setAssignOK] = useState(false);

  useEffect(() => {
    if (!apiReady) return;
    const load = async () => {
      try {
        setLoading(true);
        setError(null);
        const [teamsData, policiesData] = await Promise.all([
          listTeams(),
          listPolicies(),
        ]);
        setTeams(Array.isArray(teamsData.teams) ? teamsData.teams : []);
        setPolicies(Array.isArray(policiesData.policies) ? policiesData.policies : []);
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : "Failed to load data");
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [apiReady]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreating(true);
    setCreateError(null);
    try {
      const created = await createTeam(newName.trim(), newDesc.trim() || undefined);
      setTeams((prev) => [...prev, created]);
      setShowCreate(false);
      setNewName("");
      setNewDesc("");
    } catch (err: unknown) {
      setCreateError(err instanceof Error ? err.message : "Failed to create team");
    } finally {
      setCreating(false);
    }
  };

  const handleAssignPolicy = async (e: React.FormEvent) => {
    e.preventDefault();
    if (assignTeamID === null || !assignPolicyID) return;
    setAssigning(true);
    setAssignError(null);
    setAssignOK(false);
    try {
      await assignPolicyToTeam(assignTeamID, assignPolicyID);
      setAssignOK(true);
      setAssignTeamID(null);
      setAssignPolicyID("");
    } catch (err: unknown) {
      setAssignError(err instanceof Error ? err.message : "Failed to assign policy");
    } finally {
      setAssigning(false);
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Fleet Teams</h1>
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
            Manage FleetDM teams and assign MDM policies.
          </p>
        </div>
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 dark:bg-indigo-500 dark:hover:bg-indigo-400"
        >
          <Plus className="h-4 w-4" />
          New Team
        </button>
      </div>

      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} onDismiss={() => setError(null)} />
        </div>
      )}

      {assignOK && (
        <div className="mt-4 rounded-lg bg-green-50 border border-green-200 px-4 py-3 text-sm text-green-700 dark:bg-emerald-950 dark:border-emerald-800 dark:text-emerald-300">
          Policy assigned successfully.
        </div>
      )}

      {/* Inline create form */}
      {showCreate && (
        <form
          onSubmit={handleCreate}
          className="mt-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm space-y-3 dark:border-slate-700 dark:bg-slate-900"
        >
          <div>
            <label htmlFor="team-name" className="block text-sm font-medium text-slate-700 dark:text-slate-300">Team Name</label>
            <input
              id="team-name"
              type="text"
              required
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="e.g. Security"
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
            />
          </div>
          <div>
            <label htmlFor="team-desc" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
              Description <span className="text-slate-400">(optional)</span>
            </label>
            <input
              id="team-desc"
              type="text"
              value={newDesc}
              onChange={(e) => setNewDesc(e.target.value)}
              placeholder="e.g. Security engineering devices"
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
            />
          </div>
          {createError && (
            <p className="flex items-center gap-1 text-xs text-red-600">
              <AlertCircle className="h-3.5 w-3.5" />
              {createError}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={creating || !newName.trim()}
              className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {creating ? "Creating..." : "Create Team"}
            </button>
            <button
              type="button"
              onClick={() => { setShowCreate(false); setNewName(""); setNewDesc(""); setCreateError(null); }}
              className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {/* Policy assignment panel */}
      {assignTeamID !== null && (
        <form
          onSubmit={handleAssignPolicy}
          className="mt-4 rounded-xl border border-indigo-100 bg-indigo-50 p-4 shadow-sm space-y-3 dark:border-indigo-900 dark:bg-indigo-950"
        >
          <p className="text-sm font-medium text-indigo-800 dark:text-indigo-300">
            Assign policy to team{" "}
            <span className="font-bold">
              {teams.find((t) => t.id === assignTeamID)?.name ?? assignTeamID}
            </span>
          </p>
          <select
            value={assignPolicyID}
            onChange={(e) => setAssignPolicyID(e.target.value)}
            required
            className="w-full rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-700 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 bg-white dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
          >
            <option value="">Select a policy…</option>
            {policies.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          {assignError && (
            <p className="flex items-center gap-1 text-xs text-red-600">
              <AlertCircle className="h-3.5 w-3.5" />
              {assignError}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={assigning || !assignPolicyID}
              className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
            >
              {assigning ? "Assigning..." : "Assign Policy"}
            </button>
            <button
              type="button"
              onClick={() => { setAssignTeamID(null); setAssignPolicyID(""); setAssignError(null); }}
              className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-50 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-24 animate-pulse rounded-xl bg-slate-200 dark:bg-slate-700" />
          ))}
        </div>
      ) : (
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {teams.map((team) => (
            <div
              key={team.id}
              className="flex flex-col gap-3 rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900"
            >
              <div className="flex items-center gap-3">
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600 dark:bg-indigo-950 dark:text-indigo-400">
                  <Shield className="h-5 w-5" />
                </div>
                <div className="min-w-0">
                  <p className="font-medium text-slate-800 truncate dark:text-slate-100">{team.name}</p>
                  {team.description && (
                    <p className="text-xs text-slate-400 truncate dark:text-slate-500">{team.description}</p>
                  )}
                  <p className="text-xs text-slate-300 font-mono dark:text-slate-600">ID {team.id}</p>
                </div>
              </div>
              <button
                onClick={() => { setAssignTeamID(team.id); setAssignOK(false); }}
                className="w-full rounded-lg border border-indigo-200 bg-indigo-50 px-3 py-1.5 text-xs font-medium text-indigo-700 hover:bg-indigo-100 transition-colors dark:border-indigo-800 dark:bg-indigo-950 dark:text-indigo-400 dark:hover:bg-indigo-900"
              >
                Assign Policy
              </button>
            </div>
          ))}
          {teams.length === 0 && !loading && (
            <div className="col-span-full">
              <EmptyState
                icon={Shield}
                title="No teams yet"
                description="Create a Fleet team to manage MDM policies by group."
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
