"use client";

import { useState, FormEvent, useEffect } from "react";
import { CheckCircle, Copy, AlertCircle } from "lucide-react";
import { onboardEmployee } from "@/lib/api";

interface OnboardFormProps {
  onSuccess?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
}

export default function OnboardForm({ onSuccess, onDirtyChange }: OnboardFormProps) {
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [email, setEmail] = useState("");
  const [department, setDepartment] = useState("Engineering");
  const [role, setRole] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<{
    enrollmentToken: string;
    enrollmentUrl: string;
    warning?: string;
    nextStep?: string;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [emailError, setEmailError] = useState<string | null>(null);

  // Track dirty state: any form field filled in
  useEffect(() => {
    if (onDirtyChange) {
      const isDirty = firstName !== "" || lastName !== "" || email !== "" || role !== "";
      onDirtyChange(isDirty);
    }
  }, [firstName, lastName, email, role, onDirtyChange]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);

    try {
      const data = await onboardEmployee({ firstName, lastName, email, department, role });
      setResult({
        enrollmentToken: data.enrollmentToken || "",
        enrollmentUrl: data.enrollmentURL || "http://localhost:8080/enroll",
        warning: data.warning,
        nextStep: data.nextStep,
      });
      setCopied(false);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : "Something went wrong";
      setError(message);
    } finally {
      setSubmitting(false);
    }
  };

  // If we have a result, show credentials panel
  if (result) {
    const copyToClipboard = (text: string) => {
      navigator.clipboard.writeText(text);
      setCopied(true);
    };

    return (
      <div>
        {result.warning ? (
          <div className="flex items-start gap-3 rounded-lg bg-amber-50 p-4 text-amber-800">
            <AlertCircle className="h-5 w-5 shrink-0 text-amber-600" />
            <div>
              <p className="font-medium">User created with warnings</p>
              <p className="text-sm text-amber-700">{result.warning}</p>
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-3 rounded-lg bg-emerald-50 p-4 text-emerald-800">
            <CheckCircle className="h-5 w-5 shrink-0 text-emerald-600" />
            <div>
              <p className="font-medium">Employee onboarded successfully!</p>
              <p className="text-sm text-emerald-600">
                {firstName} {lastName} ({email})
              </p>
            </div>
          </div>
        )}

        <div className="mt-6 space-y-4">
          {/* Next Step notice */}
          {result.nextStep && (
            <div className="mt-4 rounded-lg bg-indigo-50 p-3 text-sm text-indigo-700">
              {result.nextStep}
            </div>
          )}

          {/* Enrollment Token */}
          <div>
            <label className="block text-sm font-medium text-slate-700">Enrollment Token</label>
            <div className="mt-1 flex items-center gap-2">
              <code className="flex-1 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm font-mono text-slate-800">
                {result.enrollmentToken}
              </code>
              <button
                onClick={() => copyToClipboard(result.enrollmentToken)}
                className="rounded-lg border border-slate-200 p-2 text-slate-400 hover:bg-slate-50 hover:text-slate-600 transition-colors"
                title="Copy token"
              >
                <Copy className="h-4 w-4" />
              </button>
            </div>
          </div>

          {/* Enrollment URL */}
          <div>
            <label className="block text-sm font-medium text-slate-700">Enrollment URL</label>
            <div className="mt-1 flex items-center gap-2">
              <code className="flex-1 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm font-mono text-slate-800 break-all">
                {result.enrollmentUrl}
              </code>
              <button
                onClick={() => copyToClipboard(result.enrollmentUrl)}
                className="rounded-lg border border-slate-200 p-2 text-slate-400 hover:bg-slate-50 hover:text-slate-600 transition-colors"
                title="Copy URL"
              >
                <Copy className="h-4 w-4" />
              </button>
            </div>
          </div>

          {copied && <p className="text-sm text-emerald-600">Copied to clipboard!</p>}
        </div>

        <button
          type="button"
          onClick={onSuccess}
          className="mt-6 w-full rounded-lg bg-slate-100 px-4 py-2.5 text-sm font-medium text-slate-700 transition-colors hover:bg-slate-200"
        >
          Close
        </button>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label htmlFor="firstName" className="block text-sm font-medium text-slate-700">
            First Name
          </label>
          <input
            id="firstName"
            type="text"
            required
            value={firstName}
            onChange={(e) => setFirstName(e.target.value)}
            className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            placeholder="Jane"
          />
        </div>
        <div>
          <label htmlFor="lastName" className="block text-sm font-medium text-slate-700">
            Last Name
          </label>
          <input
            id="lastName"
            type="text"
            required
            value={lastName}
            onChange={(e) => setLastName(e.target.value)}
            className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
            placeholder="Doe"
          />
        </div>
      </div>

      <div>
        <label htmlFor="email" className="block text-sm font-medium text-slate-700">
          Email
        </label>
        <input
          id="email"
          type="email"
          required
          value={email}
          onChange={(e) => {
            setEmail(e.target.value);
            if (e.target.value && !e.target.value.includes('@')) {
              setEmailError('Please enter a valid email address');
            } else {
              setEmailError(null);
            }
          }}
          className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          placeholder="jane@example.com"
        />
        {emailError && <p className="mt-1 text-xs text-red-500">{emailError}</p>}
      </div>

      <div>
        <label htmlFor="department" className="block text-sm font-medium text-slate-700">
          Department
        </label>
        <select
          id="department"
          value={department}
          onChange={(e) => setDepartment(e.target.value)}
          className="mt-1 w-full rounded-lg border border-slate-200 bg-white px-3 py-2.5 text-sm text-slate-700 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
        >
          <option>Engineering</option>
          <option>Marketing</option>
          <option>Sales</option>
          <option>Operations</option>
        </select>
      </div>

      <div>
        <label htmlFor="role" className="block text-sm font-medium text-slate-700">
          Role
        </label>
        <input
          id="role"
          type="text"
          required
          value={role}
          onChange={(e) => setRole(e.target.value)}
          className="mt-1 w-full rounded-lg border border-slate-200 px-3 py-2.5 text-sm text-slate-700 placeholder-slate-400 shadow-sm focus:border-indigo-400 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          placeholder="e.g. Software Engineer"
        />
      </div>

      {error && (
        <div className="rounded-lg bg-red-50 p-3 text-sm text-red-700">{error}</div>
      )}

      {(() => {
        const isValid = firstName.trim() !== '' && lastName.trim() !== '' && email.includes('@') && role.trim() !== '';
        return (
          <button
            type="submit"
            disabled={submitting || !isValid}
            title={!isValid ? "Please fill in all required fields correctly" : ""}
            className="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-medium text-white transition-colors hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {submitting ? "Onboarding..." : "Submit Onboarding"}
          </button>
        );
      })()}
    </form>
  );
}
