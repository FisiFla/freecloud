"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { Cloud } from "lucide-react";

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

interface FieldError {
  field: string;
  message: string;
}

export default function SetupPage() {
  const router = useRouter();
  const [adminEmail, setAdminEmail] = useState("");
  const [adminPassword, setAdminPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [orgName, setOrgName] = useState("");
  const [fieldErrors, setFieldErrors] = useState<FieldError[]>([]);
  const [globalError, setGlobalError] = useState<string | null>(null);
  const [alreadyProvisioned, setAlreadyProvisioned] = useState(false);
  const [loading, setLoading] = useState(false);

  function getFieldError(field: string): string | undefined {
    return fieldErrors.find((e) => e.field === field)?.message;
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFieldErrors([]);
    setGlobalError(null);

    // Client-side confirm password check before hitting the API.
    if (adminPassword !== confirmPassword) {
      setFieldErrors([{ field: "confirmPassword", message: "Passwords do not match" }]);
      return;
    }

    setLoading(true);
    try {
      const res = await fetch(`${API_URL}/api/v1/setup`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ adminEmail, adminPassword, orgName }),
      });

      if (res.status === 409) {
        setAlreadyProvisioned(true);
        return;
      }

      if (res.status === 400) {
        const json = await res.json() as { errors?: FieldError[] };
        setFieldErrors(json.errors ?? []);
        return;
      }

      if (!res.ok) {
        const json = await res.json().catch(() => ({})) as { error?: string };
        setGlobalError(json.error ?? "Something went wrong. Please try again.");
        return;
      }

      // Success — redirect to sign-in.
      router.push("/signin");
    } catch {
      setGlobalError("Could not reach the server. Please try again.");
    } finally {
      setLoading(false);
    }
  }

  if (alreadyProvisioned) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-50 dark:bg-slate-950">
        <div className="bg-white rounded-2xl shadow-lg p-10 w-full max-w-sm text-center dark:bg-slate-900">
          <div className="flex items-center justify-center gap-3 mb-8">
            <div className="h-10 w-10 bg-indigo-600 rounded-xl flex items-center justify-center">
              <Cloud className="h-6 w-6 text-white" />
            </div>
            <span className="text-xl font-bold text-slate-800 dark:text-slate-100">FreeCloud</span>
          </div>
          <h1 className="text-2xl font-semibold text-slate-900 mb-2 dark:text-slate-100">Already set up</h1>
          <p className="text-slate-500 mb-8 dark:text-slate-400">
            This instance is already provisioned.
          </p>
          <a
            href="/signin"
            className="w-full block py-3 px-4 bg-indigo-600 hover:bg-indigo-700 text-white font-medium rounded-xl transition-colors text-center dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            Go to sign in
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-50 dark:bg-slate-950">
      <div className="bg-white rounded-2xl shadow-lg p-10 w-full max-w-sm dark:bg-slate-900">
        <div className="flex items-center justify-center gap-3 mb-8">
          <div className="h-10 w-10 bg-indigo-600 rounded-xl flex items-center justify-center">
            <Cloud className="h-6 w-6 text-white" />
          </div>
          <span className="text-xl font-bold text-slate-800 dark:text-slate-100">FreeCloud</span>
        </div>
        <h1 className="text-2xl font-semibold text-slate-900 mb-1 dark:text-slate-100">Welcome</h1>
        <p className="text-slate-500 mb-8 dark:text-slate-400">Set up your control plane</p>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-slate-700 mb-1 dark:text-slate-300">
              Organization name
            </label>
            <input
              type="text"
              value={orgName}
              onChange={(e) => setOrgName(e.target.value)}
              placeholder="Acme Corp"
              className="w-full px-3 py-2 border border-slate-300 rounded-xl text-slate-900 placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:bg-slate-800 dark:border-slate-600 dark:text-slate-100"
              required
            />
            {getFieldError("orgName") && (
              <p className="mt-1 text-sm text-red-600 dark:text-red-400">{getFieldError("orgName")}</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700 mb-1 dark:text-slate-300">
              Admin email
            </label>
            <input
              type="email"
              value={adminEmail}
              onChange={(e) => setAdminEmail(e.target.value)}
              placeholder="admin@example.com"
              className="w-full px-3 py-2 border border-slate-300 rounded-xl text-slate-900 placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:bg-slate-800 dark:border-slate-600 dark:text-slate-100"
              required
            />
            {getFieldError("adminEmail") && (
              <p className="mt-1 text-sm text-red-600 dark:text-red-400">{getFieldError("adminEmail")}</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700 mb-1 dark:text-slate-300">
              Password
            </label>
            <input
              type="password"
              value={adminPassword}
              onChange={(e) => setAdminPassword(e.target.value)}
              placeholder="Min. 8 characters"
              className="w-full px-3 py-2 border border-slate-300 rounded-xl text-slate-900 placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:bg-slate-800 dark:border-slate-600 dark:text-slate-100"
              required
            />
            {getFieldError("adminPassword") && (
              <p className="mt-1 text-sm text-red-600 dark:text-red-400">{getFieldError("adminPassword")}</p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-slate-700 mb-1 dark:text-slate-300">
              Confirm password
            </label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder="Repeat password"
              className="w-full px-3 py-2 border border-slate-300 rounded-xl text-slate-900 placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:bg-slate-800 dark:border-slate-600 dark:text-slate-100"
              required
            />
            {getFieldError("confirmPassword") && (
              <p className="mt-1 text-sm text-red-600 dark:text-red-400">{getFieldError("confirmPassword")}</p>
            )}
          </div>

          {globalError && (
            <p className="text-sm text-red-600 dark:text-red-400">{globalError}</p>
          )}

          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 px-4 bg-indigo-600 hover:bg-indigo-700 disabled:opacity-60 text-white font-medium rounded-xl transition-colors dark:bg-indigo-500 dark:hover:bg-indigo-400"
          >
            {loading ? "Setting up…" : "Set up FreeCloud"}
          </button>
        </form>
      </div>
    </div>
  );
}
