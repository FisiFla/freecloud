"use client";

import { useState } from "react";
import { Plus, Globe, ToggleLeft, ToggleRight } from "lucide-react";
import SlideOver from "@/components/SlideOver";

interface App {
  id: string;
  name: string;
  protocol: "OIDC" | "SAML";
  baseUrl: string;
  enabled: boolean;
}

const mockApps: App[] = [
  { id: "a1", name: "Google Workspace", protocol: "OIDC", baseUrl: "https://accounts.google.com", enabled: true },
  { id: "a2", name: "Slack", protocol: "OIDC", baseUrl: "https://slack.com", enabled: true },
  { id: "a3", name: "GitHub Enterprise", protocol: "SAML", baseUrl: "https://github.com", enabled: false },
  { id: "a4", name: "Okta", protocol: "OIDC", baseUrl: "https://okta.com", enabled: true },
];

export default function AppsPage() {
  const [apps, setApps] = useState<App[]>(mockApps);
  const [showAdd, setShowAdd] = useState(false);
  const [newName, setNewName] = useState("");
  const [newProtocol, setNewProtocol] = useState<"OIDC" | "SAML">("OIDC");
  const [newRedirectUris, setNewRedirectUris] = useState("");
  const [newBaseUrl, setNewBaseUrl] = useState("");

  const toggleApp = (id: string) => {
    setApps((prev) => prev.map((a) => (a.id === id ? { ...a, enabled: !a.enabled } : a)));
  };

  const handleAddApp = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await fetch("http://localhost:8080/api/v1/apps/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: newName,
          protocol: newProtocol,
          redirect_uris: newRedirectUris.split("\n").map((s) => s.trim()).filter(Boolean),
          base_url: newBaseUrl,
        }),
      });
      // For now just add to local state
      setApps((prev) => [
        ...prev,
        {
          id: `a${Date.now()}`,
          name: newName,
          protocol: newProtocol,
          baseUrl: newBaseUrl,
          enabled: true,
        },
      ]);
      setShowAdd(false);
      setNewName("");
      setNewProtocol("OIDC");
      setNewRedirectUris("");
      setNewBaseUrl("");
    } catch {
      alert("Failed to create app. Check backend is running.");
    }
  };

  return (
    <>
      <div>
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-slate-800">App Catalog</h1>
            <p className="mt-1 text-sm text-slate-500">Manage SSO-connected applications.</p>
          </div>
          <button
            onClick={() => setShowAdd(true)}
            className="flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
          >
            <Plus className="h-4 w-4" />
            Add Application
          </button>
        </div>

        {/* Grid */}
        <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((app) => (
            <div
              key={app.id}
              className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition-shadow hover:shadow-md"
            >
              <div className="flex items-start justify-between">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
                  <Globe className="h-5 w-5" />
                </div>
                <button
                  onClick={() => toggleApp(app.id)}
                  className={`transition-colors ${
                    app.enabled ? "text-indigo-600" : "text-slate-300"
                  }`}
                  title={app.enabled ? "Disable" : "Enable"}
                >
                  {app.enabled ? (
                    <ToggleRight className="h-6 w-6" />
                  ) : (
                    <ToggleLeft className="h-6 w-6" />
                  )}
                </button>
              </div>

              <h3 className="mt-4 font-semibold text-slate-800">{app.name}</h3>
              <p className="mt-1 text-xs text-slate-500 truncate">{app.baseUrl}</p>

              <div className="mt-4 flex items-center gap-2">
                <span
                  className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    app.protocol === "OIDC"
                      ? "bg-sky-50 text-sky-700"
                      : "bg-amber-50 text-amber-700"
                  }`}
                >
                  {app.protocol}
                </span>
                <span
                  className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    app.enabled
                      ? "bg-emerald-50 text-emerald-700"
                      : "bg-slate-100 text-slate-500"
                  }`}
                >
                  {app.enabled ? "Enabled" : "Disabled"}
                </span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Add App Slide Over */}
      <SlideOver isOpen={showAdd} onClose={() => setShowAdd(false)} title="Add Application">
        <form onSubmit={handleAddApp} className="space-y-5">
          <div>
            <label className="block text-sm font-medium text-slate-700">Application Name</label>
            <input
              type="text"
              required
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="My App"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700">Protocol</label>
            <div className="mt-2 flex gap-4">
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="radio"
                  name="protocol"
                  value="OIDC"
                  checked={newProtocol === "OIDC"}
                  onChange={() => setNewProtocol("OIDC")}
                  className="text-indigo-600 focus:ring-indigo-500"
                />
                OIDC
              </label>
              <label className="flex items-center gap-2 text-sm text-slate-700">
                <input
                  type="radio"
                  name="protocol"
                  value="SAML"
                  checked={newProtocol === "SAML"}
                  onChange={() => setNewProtocol("SAML")}
                  className="text-indigo-600 focus:ring-indigo-500"
                />
                SAML
              </label>
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700">
              Redirect URIs <span className="text-slate-400">(one per line)</span>
            </label>
            <textarea
              rows={4}
              value={newRedirectUris}
              onChange={(e) => setNewRedirectUris(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="https://myapp.com/callback"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700">Base URL</label>
            <input
              type="url"
              required
              value={newBaseUrl}
              onChange={(e) => setNewBaseUrl(e.target.value)}
              className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
              placeholder="https://myapp.com"
            />
          </div>

          <button
            type="submit"
            className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
          >
            Create Application
          </button>
        </form>
      </SlideOver>
    </>
  );
}
