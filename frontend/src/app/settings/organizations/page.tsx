"use client";

import { useCallback, useEffect, useState } from "react";
import { Building2, Plus, UserPlus, Trash2, AlertCircle } from "lucide-react";
import ErrorBanner from "@/components/ErrorBanner";
import EmptyState from "@/components/EmptyState";
import ConfirmDialog from "@/components/ConfirmDialog";
import {
  listOrgs,
  createOrg,
  listOrgMembers,
  addOrgMember,
  removeOrgMember,
} from "@/lib/api";
import type { Org, OrgMember } from "@/lib/api";
import { useApiReady, useOrg } from "../../providers";

/**
 * Organizations settings page (Epic C multi-tenant).
 *
 * System-admins see the full org list and can create new orgs; org-admins
 * see only their own org's membership. The backend enforces this
 * distinction (403 on cross-org member management) — the UI mirrors it so
 * an org-admin isn't shown controls they cannot use.
 */
export default function OrganizationsPage() {
  const apiReady = useApiReady();
  const { me } = useOrg();

  const [orgs, setOrgs] = useState<Org[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Create-org form (system-admin only)
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  // Selected org for member management
  const [selectedOrgId, setSelectedOrgId] = useState<string | null>(null);
  const [members, setMembers] = useState<OrgMember[]>([]);
  const [membersLoading, setMembersLoading] = useState(false);
  const [membersError, setMembersError] = useState<string | null>(null);

  // Add-member form
  const [newMemberUserId, setNewMemberUserId] = useState("");
  const [newMemberRole, setNewMemberRole] = useState<"member" | "org-admin">("member");
  const [addingMember, setAddingMember] = useState(false);
  const [addMemberError, setAddMemberError] = useState<string | null>(null);

  const [removeTarget, setRemoveTarget] = useState<OrgMember | null>(null);
  const [removing, setRemoving] = useState(false);

  const isSystemAdmin = !!me?.isSystemAdmin;

  const fetchOrgs = useCallback(async () => {
    if (!isSystemAdmin) {
      // Org-admins don't have list-all-orgs access; scope the page to their
      // own active org instead (from /me, already resolved by OrgProvider).
      if (me) {
        setOrgs(
          me.orgs.map((o) => ({ id: o.orgId, name: o.orgName, slug: o.orgSlug, createdAt: "" })),
        );
        setSelectedOrgId(me.activeOrgId || null);
      }
      setLoading(false);
      return;
    }
    try {
      setLoading(true);
      setError(null);
      const result = await listOrgs();
      setOrgs(result);
      if (result.length > 0 && !selectedOrgId) setSelectedOrgId(result[0].id);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to load organizations");
    } finally {
      setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isSystemAdmin, me]);

  useEffect(() => {
    if (!apiReady || !me) return;
    fetchOrgs();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiReady, me]);

  const fetchMembers = useCallback(async (orgId: string) => {
    try {
      setMembersLoading(true);
      setMembersError(null);
      const result = await listOrgMembers(orgId);
      setMembers(result);
    } catch (err: unknown) {
      setMembersError(err instanceof Error ? err.message : "Failed to load members");
    } finally {
      setMembersLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!selectedOrgId) return;
    fetchMembers(selectedOrgId);
  }, [selectedOrgId, fetchMembers]);

  const handleCreateOrg = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !slug.trim()) return;
    try {
      setCreating(true);
      setCreateError(null);
      await createOrg({ name: name.trim(), slug: slug.trim() });
      setName("");
      setSlug("");
      await fetchOrgs();
    } catch (err: unknown) {
      setCreateError(err instanceof Error ? err.message : "Failed to create organization");
    } finally {
      setCreating(false);
    }
  };

  const handleAddMember = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedOrgId || !newMemberUserId.trim()) return;
    try {
      setAddingMember(true);
      setAddMemberError(null);
      await addOrgMember(selectedOrgId, newMemberUserId.trim(), newMemberRole);
      setNewMemberUserId("");
      setNewMemberRole("member");
      await fetchMembers(selectedOrgId);
    } catch (err: unknown) {
      setAddMemberError(err instanceof Error ? err.message : "Failed to add member");
    } finally {
      setAddingMember(false);
    }
  };

  const handleRemoveMember = async () => {
    if (!selectedOrgId || !removeTarget) return;
    try {
      setRemoving(true);
      await removeOrgMember(selectedOrgId, removeTarget.userId);
      setRemoveTarget(null);
      await fetchMembers(selectedOrgId);
    } catch (err: unknown) {
      setMembersError(err instanceof Error ? err.message : "Failed to remove member");
      setRemoveTarget(null);
    } finally {
      setRemoving(false);
    }
  };

  return (
    <div>
      <div className="flex items-center gap-3">
        <Building2 className="h-6 w-6 text-indigo-600 dark:text-indigo-400" />
        <h1 className="text-2xl font-bold text-slate-800 dark:text-slate-100">Organizations</h1>
      </div>
      <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
        {isSystemAdmin
          ? "Create organizations and manage membership across every tenant in this deployment."
          : "Manage membership for your organization."}
      </p>

      {error && (
        <div className="mt-4">
          <ErrorBanner message={error} />
        </div>
      )}

      {/* Create-org form — system-admin only */}
      {isSystemAdmin && (
        <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-900">
          <h2 className="font-semibold text-slate-800 dark:text-slate-100">Create Organization</h2>
          <form onSubmit={handleCreateOrg} className="mt-4 grid gap-4 sm:grid-cols-2">
            <div>
              <label htmlFor="org-name" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Name
              </label>
              <input
                id="org-name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. Acme Corp"
                required
                maxLength={200}
                className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
              />
            </div>
            <div>
              <label htmlFor="org-slug" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Slug
              </label>
              <input
                id="org-slug"
                type="text"
                value={slug}
                onChange={(e) => setSlug(e.target.value.toLowerCase())}
                placeholder="e.g. acme-corp"
                required
                maxLength={63}
                pattern="[a-z0-9]+(-[a-z0-9]+)*"
                className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
              />
            </div>
            {createError && (
              <div className="col-span-2 flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                <AlertCircle className="h-4 w-4 shrink-0" />
                {createError}
              </div>
            )}
            <div className="col-span-2">
              <button
                type="submit"
                disabled={creating || !name.trim() || !slug.trim()}
                className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-5 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
              >
                <Plus className="h-4 w-4" />
                {creating ? "Creating…" : "Create Organization"}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Org list + member management */}
      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <div className="rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900 lg:col-span-1">
          <div className="border-b border-slate-100 px-6 py-4 dark:border-slate-800">
            <h2 className="font-semibold text-slate-800 dark:text-slate-100">
              {isSystemAdmin ? "All Organizations" : "Your Organization"}
            </h2>
          </div>
          {loading ? (
            <div className="space-y-2 p-4">
              {Array.from({ length: 3 }).map((_, i) => (
                <div key={i} className="h-10 animate-pulse rounded-lg bg-slate-100 dark:bg-slate-800" />
              ))}
            </div>
          ) : orgs.length === 0 ? (
            <div className="p-6">
              <EmptyState icon={Building2} title="No organizations yet" />
            </div>
          ) : (
            <ul className="divide-y divide-slate-100 dark:divide-slate-800" role="list">
              {orgs.map((o) => (
                <li key={o.id}>
                  <button
                    onClick={() => setSelectedOrgId(o.id)}
                    aria-current={selectedOrgId === o.id ? "true" : undefined}
                    className={`w-full px-6 py-3 text-left text-sm transition-colors ${
                      selectedOrgId === o.id
                        ? "bg-indigo-50 text-indigo-700 dark:bg-indigo-950 dark:text-indigo-300"
                        : "text-slate-600 hover:bg-slate-50 dark:text-slate-300 dark:hover:bg-slate-800"
                    }`}
                  >
                    <p className="font-medium">{o.name}</p>
                    <p className="text-xs text-slate-400 dark:text-slate-500">{o.slug}</p>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="rounded-xl border border-slate-200 bg-white shadow-sm dark:border-slate-700 dark:bg-slate-900 lg:col-span-2">
          <div className="border-b border-slate-100 px-6 py-4 dark:border-slate-800">
            <h2 className="font-semibold text-slate-800 dark:text-slate-100">Members</h2>
          </div>

          {!selectedOrgId ? (
            <div className="p-6">
              <EmptyState icon={UserPlus} title="Select an organization to manage its members" />
            </div>
          ) : (
            <>
              {membersError && (
                <div className="p-6 pb-0">
                  <ErrorBanner message={membersError} />
                </div>
              )}

              {/* Add-member form */}
              <form onSubmit={handleAddMember} className="flex flex-wrap items-end gap-3 border-b border-slate-100 p-6 dark:border-slate-800">
                <div className="flex-1 min-w-[200px]">
                  <label htmlFor="member-user-id" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    User ID
                  </label>
                  <input
                    id="member-user-id"
                    type="text"
                    value={newMemberUserId}
                    onChange={(e) => setNewMemberUserId(e.target.value)}
                    placeholder="Keycloak user UUID"
                    required
                    className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100 dark:placeholder-slate-500"
                  />
                </div>
                <div>
                  <label htmlFor="member-role" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Role
                  </label>
                  <select
                    id="member-role"
                    value={newMemberRole}
                    onChange={(e) => setNewMemberRole(e.target.value as "member" | "org-admin")}
                    className="mt-1 rounded-lg border border-slate-200 px-3 py-2 text-sm shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100"
                  >
                    <option value="member">member</option>
                    <option value="org-admin">org-admin</option>
                  </select>
                </div>
                <button
                  type="submit"
                  disabled={addingMember || !newMemberUserId.trim()}
                  className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed dark:bg-indigo-500 dark:hover:bg-indigo-400"
                >
                  <UserPlus className="h-4 w-4" />
                  {addingMember ? "Adding…" : "Add Member"}
                </button>
                {addMemberError && (
                  <div className="w-full flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                    <AlertCircle className="h-4 w-4 shrink-0" />
                    {addMemberError}
                  </div>
                )}
              </form>

              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="bg-slate-50 text-xs font-medium uppercase text-slate-500 dark:bg-slate-800 dark:text-slate-400">
                    <tr>
                      <th className="px-6 py-3 text-left">Name</th>
                      <th className="px-6 py-3 text-left">Email</th>
                      <th className="px-6 py-3 text-left">Role</th>
                      <th className="px-6 py-3 text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                    {membersLoading ? (
                      Array.from({ length: 3 }).map((_, i) => (
                        <tr key={i}>
                          {Array.from({ length: 4 }).map((__, j) => (
                            <td key={j} className="px-6 py-3">
                              <div className="h-4 animate-pulse rounded bg-slate-200 dark:bg-slate-700" />
                            </td>
                          ))}
                        </tr>
                      ))
                    ) : members.length === 0 ? (
                      <tr>
                        <td colSpan={4} className="px-6 py-8 text-center text-slate-400 dark:text-slate-500">
                          No members yet.
                        </td>
                      </tr>
                    ) : (
                      members.map((m) => (
                        <tr key={m.userId} className="hover:bg-slate-50 dark:hover:bg-slate-800">
                          <td className="px-6 py-3 font-medium text-slate-800 dark:text-slate-100">
                            {m.firstName} {m.lastName}
                          </td>
                          <td className="px-6 py-3 text-slate-600 dark:text-slate-400">{m.email}</td>
                          <td className="px-6 py-3">
                            <span className="inline-flex items-center rounded-full bg-indigo-50 px-2.5 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-950 dark:text-indigo-300">
                              {m.role}
                            </span>
                          </td>
                          <td className="px-6 py-3 text-right">
                            <button
                              onClick={() => setRemoveTarget(m)}
                              className="inline-flex items-center gap-1 rounded-lg border border-red-200 bg-red-50 px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-100 dark:border-red-800 dark:bg-red-950 dark:text-red-300 dark:hover:bg-red-900"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                              Remove
                            </button>
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </div>
      </div>

      <ConfirmDialog
        isOpen={!!removeTarget}
        title="Remove Member"
        message={`Remove ${removeTarget?.firstName} ${removeTarget?.lastName} from this organization?`}
        confirmLabel={removing ? "Removing…" : "Remove"}
        variant="danger"
        onConfirm={handleRemoveMember}
        onClose={() => setRemoveTarget(null)}
      />
    </div>
  );
}
