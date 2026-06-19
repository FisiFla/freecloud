"use client";

import { useState, FormEvent } from "react";
import { Cloud, CheckCircle, AlertCircle } from "lucide-react";
import { forgotPassword } from "@/lib/api";

type State = "idle" | "loading" | "sent" | "error";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [state, setState] = useState<State>("idle");
  const [errorMsg, setErrorMsg] = useState("");

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!email.trim()) return;
    setState("loading");
    setErrorMsg("");
    try {
      await forgotPassword(email.trim());
      // Backend always returns 200 with a generic message — treat as success.
      setState("sent");
    } catch (err: unknown) {
      // Network errors, etc.
      setState("error");
      setErrorMsg(err instanceof Error ? err.message : "An unexpected error occurred.");
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-50">
      <div className="bg-white rounded-2xl shadow-lg p-10 w-full max-w-sm text-center">
        <div className="flex items-center justify-center gap-3 mb-8">
          <div className="h-10 w-10 bg-indigo-600 rounded-xl flex items-center justify-center">
            <Cloud className="h-6 w-6 text-white" />
          </div>
          <span className="text-xl font-bold text-slate-800">FreeCloud</span>
        </div>

        {state === "sent" ? (
          <div className="text-center">
            <CheckCircle className="mx-auto h-10 w-10 text-emerald-500" />
            <h1 className="mt-4 text-xl font-semibold text-slate-900">Check your email</h1>
            <p className="mt-2 text-sm text-slate-500">
              If an account with that email exists, a password reset link has been sent.
            </p>
            <a
              href="/signin"
              className="mt-6 inline-block w-full rounded-xl bg-indigo-600 py-3 px-4 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
            >
              Back to sign in
            </a>
          </div>
        ) : (
          <>
            <h1 className="text-2xl font-semibold text-slate-900 mb-2">Forgot password?</h1>
            <p className="text-slate-500 mb-8 text-sm">
              Enter your email and we&apos;ll send a reset link if an account exists.
            </p>

            <form onSubmit={handleSubmit} className="space-y-4 text-left">
              <div>
                <label htmlFor="email" className="block text-sm font-medium text-slate-700">
                  Email address
                </label>
                <input
                  id="email"
                  type="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
                />
              </div>

              {state === "error" && (
                <div className="flex items-center gap-2 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700">
                  <AlertCircle className="h-4 w-4 shrink-0" />
                  {errorMsg}
                </div>
              )}

              <button
                type="submit"
                disabled={state === "loading" || !email.trim()}
                className="w-full rounded-xl bg-indigo-600 py-3 px-4 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {state === "loading" ? "Sending…" : "Send reset link"}
              </button>
            </form>

            <a
              href="/signin"
              className="mt-6 inline-block text-sm text-indigo-600 hover:text-indigo-800"
            >
              &larr; Back to sign in
            </a>
          </>
        )}
      </div>
    </div>
  );
}
