"use client";

// D2 — Per-app access policy tab.
// Lets admins configure posture + condition gates (time window, network, geo)
// and preview what the policy decision would be for a given scenario.

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import ErrorBanner from "@/components/ErrorBanner";
import {
  getAppPolicy,
  upsertAppPolicy,
  previewAppPolicy,
  type AppAccessPolicy,
  type AccessEvalResponse,
} from "@/lib/api";
import { useApiReady } from "../../../providers";

export default function AppPolicyPage() {
  const { id: appId } = useParams<{ id: string }>();
  const apiReady = useApiReady();

  // Policy load state
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Policy form fields
  const [requireEnrolled, setRequireEnrolled] = useState(false);
  const [requireDiskEncrypted, setRequireDiskEncrypted] = useState(false);
  const [requireNoCriticalVulns, setRequireNoCriticalVulns] = useState(false);
  const [allowedTimeStart, setAllowedTimeStart] = useState("");
  const [allowedTimeEnd, setAllowedTimeEnd] = useState("");
  const [networkAllowlistRaw, setNetworkAllowlistRaw] = useState(""); // newline-separated
  const [geoCountryAllowlistRaw, setGeoCountryAllowlistRaw] = useState(""); // newline-separated

  // Save state
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveOk, setSaveOk] = useState(false);

  // Preview form fields
  const [previewIP, setPreviewIP] = useState("");
  const [previewTime, setPreviewTime] = useState("");
  const [previewUserId, setPreviewUserId] = useState("");
  const [previewDeviceId, setPreviewDeviceId] = useState("");

  // Preview results
  const [previewing, setPreviewing] = useState(false);
  const [previewResult, setPreviewResult] = useState<AccessEvalResponse | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);

  useEffect(() => {
    if (!apiReady || !appId) return;
    const load = async () => {
      try {
        setLoading(true);
        setLoadError(null);
        const policy = await getAppPolicy(appId);
        setRequireEnrolled(policy.requireEnrolled);
        setRequireDiskEncrypted(policy.requireDiskEncrypted);
        setRequireNoCriticalVulns(policy.requireNoCriticalVulns);
        setAllowedTimeStart(policy.allowedTimeStart ?? "");
        setAllowedTimeEnd(policy.allowedTimeEnd ?? "");
        setNetworkAllowlistRaw((policy.networkAllowlist ?? []).join("\n"));
        setGeoCountryAllowlistRaw((policy.geoCountryAllowlist ?? []).join("\n"));
      } catch (err: unknown) {
        setLoadError(err instanceof Error ? err.message : "Failed to load policy");
      } finally {
        setLoading(false);
      }
    };
    load();
  }, [apiReady, appId]);

  const parseLines = (raw: string): string[] =>
    raw
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!appId) return;
    setSaving(true);
    setSaveError(null);
    setSaveOk(false);
    try {
      const policy: Omit<AppAccessPolicy, "appId" | "updatedAt"> = {
        requireEnrolled,
        requireDiskEncrypted,
        requireNoCriticalVulns,
        allowedTimeStart: allowedTimeStart.trim() || undefined,
        allowedTimeEnd: allowedTimeEnd.trim() || undefined,
        networkAllowlist: parseLines(networkAllowlistRaw),
        geoCountryAllowlist: parseLines(geoCountryAllowlistRaw),
      };
      await upsertAppPolicy(appId, policy);
      setSaveOk(true);
      setTimeout(() => setSaveOk(false), 3000);
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : "Failed to save policy");
    } finally {
      setSaving(false);
    }
  };

  const handlePreview = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!appId) return;
    setPreviewing(true);
    setPreviewResult(null);
    setPreviewError(null);
    try {
      const result = await previewAppPolicy(appId, {
        clientIp: previewIP.trim() || undefined,
        evalTime: previewTime.trim() || undefined,
        userId: previewUserId.trim() || undefined,
        deviceId: previewDeviceId.trim() || undefined,
      });
      setPreviewResult(result);
    } catch (err: unknown) {
      setPreviewError(err instanceof Error ? err.message : "Preview failed");
    } finally {
      setPreviewing(false);
    }
  };

  if (loading) {
    return <div className="p-6 text-sm text-gray-500">Loading policy…</div>;
  }

  return (
    <div className="p-6 max-w-3xl space-y-8">
      <div>
        <h1 className="text-xl font-semibold text-gray-900">Access Policy</h1>
        <p className="mt-1 text-sm text-gray-500">
          Configure posture requirements and conditional-access rules for this app.
        </p>
      </div>

      {loadError && <ErrorBanner message={loadError} />}

      {/* Policy form */}
      <form onSubmit={handleSave} className="space-y-6 bg-white border border-gray-200 rounded-lg p-5">
        {/* Posture requirements */}
        <div>
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3">
            Posture Requirements
          </h2>
          <div className="space-y-2">
            {[
              {
                id: "requireEnrolled",
                label: "Require device enrolled",
                checked: requireEnrolled,
                onChange: setRequireEnrolled,
              },
              {
                id: "requireDiskEncrypted",
                label: "Require disk encryption",
                checked: requireDiskEncrypted,
                onChange: setRequireDiskEncrypted,
              },
              {
                id: "requireNoCriticalVulns",
                label: "Require no critical vulnerabilities",
                checked: requireNoCriticalVulns,
                onChange: setRequireNoCriticalVulns,
              },
            ].map(({ id, label, checked, onChange }) => (
              <label key={id} className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
                <input
                  type="checkbox"
                  id={id}
                  checked={checked}
                  onChange={(e) => onChange(e.target.checked)}
                  className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                {label}
              </label>
            ))}
          </div>
        </div>

        {/* Time window */}
        <div>
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3">
            Time Window (UTC)
          </h2>
          <p className="text-xs text-gray-500 mb-3">
            Leave both fields empty for no time restriction. Format: HH:MM (24-hour UTC).
          </p>
          <div className="flex items-center gap-4">
            <div>
              <label htmlFor="timeStart" className="block text-xs font-medium text-gray-600 mb-1">
                Start
              </label>
              <input
                id="timeStart"
                type="text"
                placeholder="09:00"
                value={allowedTimeStart}
                onChange={(e) => setAllowedTimeStart(e.target.value)}
                className="block w-28 rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <span className="mt-4 text-gray-400">–</span>
            <div>
              <label htmlFor="timeEnd" className="block text-xs font-medium text-gray-600 mb-1">
                End
              </label>
              <input
                id="timeEnd"
                type="text"
                placeholder="18:00"
                value={allowedTimeEnd}
                onChange={(e) => setAllowedTimeEnd(e.target.value)}
                className="block w-28 rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>
        </div>

        {/* Network allowlist */}
        <div>
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3">
            Network Allowlist
          </h2>
          <p className="text-xs text-gray-500 mb-2">
            One IP or CIDR per line (e.g. <code>10.0.0.0/8</code> or <code>1.2.3.4</code>).
            Leave empty for no network restriction.
          </p>
          <textarea
            value={networkAllowlistRaw}
            onChange={(e) => setNetworkAllowlistRaw(e.target.value)}
            rows={4}
            placeholder={"10.0.0.0/8\n192.168.1.0/24"}
            className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm font-mono shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {/* Geo country allowlist */}
        <div>
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide mb-3">
            Geo Country Allowlist
          </h2>
          <p className="text-xs text-gray-500 mb-2">
            One ISO 3166-1 alpha-2 country code per line (e.g. <code>DE</code>, <code>AT</code>).
            Leave empty for no geo restriction.{" "}
            <strong>Note:</strong> requires a GeoIP plugin — without one all geo conditions fail closed.
          </p>
          <textarea
            value={geoCountryAllowlistRaw}
            onChange={(e) => setGeoCountryAllowlistRaw(e.target.value)}
            rows={3}
            placeholder={"DE\nAT\nCH"}
            className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm font-mono shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {saveError && <p className="text-sm text-red-600">{saveError}</p>}
        {saveOk && <p className="text-sm text-green-600">Policy saved.</p>}

        <button
          type="submit"
          disabled={saving}
          className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-blue-700 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          {saving ? "Saving…" : "Save policy"}
        </button>
      </form>

      {/* Preview / test section */}
      <div className="bg-white border border-gray-200 rounded-lg p-5 space-y-4">
        <div>
          <h2 className="text-sm font-semibold text-gray-700 uppercase tracking-wide">
            Preview / Test
          </h2>
          <p className="mt-1 text-xs text-gray-500">
            Dry-run: simulates what the policy decision would be for a given scenario. No audit log
            is written.
          </p>
        </div>

        <form onSubmit={handlePreview} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label htmlFor="previewIP" className="block text-xs font-medium text-gray-600 mb-1">
                Client IP
              </label>
              <input
                id="previewIP"
                type="text"
                placeholder="1.2.3.4"
                value={previewIP}
                onChange={(e) => setPreviewIP(e.target.value)}
                className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label htmlFor="previewTime" className="block text-xs font-medium text-gray-600 mb-1">
                Eval time (RFC3339, blank = now)
              </label>
              <input
                id="previewTime"
                type="text"
                placeholder="2025-01-01T12:00:00Z"
                value={previewTime}
                onChange={(e) => setPreviewTime(e.target.value)}
                className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label htmlFor="previewUserId" className="block text-xs font-medium text-gray-600 mb-1">
                User ID (optional, for posture check)
              </label>
              <input
                id="previewUserId"
                type="text"
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                value={previewUserId}
                onChange={(e) => setPreviewUserId(e.target.value)}
                className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
            <div>
              <label htmlFor="previewDeviceId" className="block text-xs font-medium text-gray-600 mb-1">
                Device ID (optional, for posture check)
              </label>
              <input
                id="previewDeviceId"
                type="text"
                placeholder="fleet-host-id"
                value={previewDeviceId}
                onChange={(e) => setPreviewDeviceId(e.target.value)}
                className="block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>
          </div>

          {previewError && <p className="text-sm text-red-600">{previewError}</p>}

          <button
            type="submit"
            disabled={previewing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
          >
            {previewing ? "Running preview…" : "Run preview"}
          </button>

          {previewResult && (
            <div
              className={`rounded-md p-4 text-sm ${
                previewResult.allow
                  ? "bg-green-50 border border-green-200 text-green-800"
                  : "bg-red-50 border border-red-200 text-red-800"
              }`}
            >
              <p className="font-semibold">
                {previewResult.allow ? "ALLOW" : "DENY"}
              </p>
              {previewResult.reasons && previewResult.reasons.length > 0 && (
                <ul className="mt-2 list-disc pl-4 space-y-1">
                  {previewResult.reasons.map((reason, i) => (
                    <li key={i}>{reason}</li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </form>
      </div>
    </div>
  );
}
