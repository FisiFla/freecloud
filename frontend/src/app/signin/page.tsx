"use client";

import { signIn } from "next-auth/react";
import { Cloud } from "lucide-react";

export default function SignIn() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-50 dark:bg-slate-950">
      <div className="bg-white rounded-2xl shadow-lg p-10 w-full max-w-sm text-center dark:bg-slate-900">
        <div className="flex items-center justify-center gap-3 mb-8">
          <div className="h-10 w-10 bg-indigo-600 rounded-xl flex items-center justify-center">
            <Cloud className="h-6 w-6 text-white" />
          </div>
          <span className="text-xl font-bold text-slate-800 dark:text-slate-100">FreeCloud</span>
        </div>
        <h1 className="text-2xl font-semibold text-slate-900 mb-2 dark:text-slate-100">Welcome back</h1>
        <p className="text-slate-500 mb-8 dark:text-slate-400">Sign in to your control plane</p>

        <button
          onClick={() => signIn("keycloak", { callbackUrl: "/" })}
          className="w-full py-3 px-4 bg-indigo-600 hover:bg-indigo-700 text-white font-medium rounded-xl transition-colors dark:bg-indigo-500 dark:hover:bg-indigo-400"
        >
          Sign in with Keycloak
        </button>

        <a
          href="/forgot-password"
          className="mt-4 block text-sm text-indigo-600 hover:text-indigo-800 dark:text-indigo-400 dark:hover:text-indigo-300"
        >
          Forgot password?
        </a>
      </div>
    </div>
  );
}
