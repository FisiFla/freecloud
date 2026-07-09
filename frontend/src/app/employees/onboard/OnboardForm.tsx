"use client";

import { useState, FormEvent, useEffect } from "react";
import { CheckCircle, Copy, AlertCircle } from "lucide-react";
import { ApiError, onboardEmployee } from "@/lib/api";
import { Button, Field, Input, Select } from "@/components/ui";

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
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

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
    setFieldErrors({});

    try {
      const data = await onboardEmployee({ firstName, lastName, email, department, role });
      setResult({
        enrollmentToken: data.enrollmentToken || "",
        enrollmentUrl: data.enrollmentURL || "",
        warning: data.warning,
        nextStep: data.nextStep,
      });
      setCopied(false);
    } catch (err: unknown) {
      if (err instanceof ApiError && err.fieldErrors && err.fieldErrors.length > 0) {
        const mapped: Record<string, string> = {};
        for (const fe of err.fieldErrors) {
          mapped[fe.field] = fe.message;
        }
        setFieldErrors(mapped);
      } else {
        const message = err instanceof Error ? err.message : "Something went wrong";
        setFieldErrors({});
        setError(message);
      }
    } finally {
      setSubmitting(false);
    }
  };

  // If we have a result, show credentials panel
  if (result) {
    const copyToClipboard = async (text: string) => {
      try {
        await navigator.clipboard.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      } catch {
        setError("Could not copy to clipboard — check browser permissions.");
      }
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
          {result.nextStep && (
            <div className="mt-4 rounded-lg bg-indigo-50 p-3 text-sm text-indigo-700">
              {result.nextStep}
            </div>
          )}

          {/* Enrollment Token */}
          <Field label="Enrollment Token">
            <div className="mt-1 flex items-center gap-2">
              <code className="flex-1 rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-sm text-slate-800">
                {result.enrollmentToken}
              </code>
              <Button
                variant="ghost"
                onClick={() => copyToClipboard(result.enrollmentToken)}
                title="Copy token"
                className="p-2"
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          </Field>

          {result.enrollmentUrl && (
            <Field label="Enrollment URL">
              <div className="mt-1 flex items-center gap-2">
                <code className="flex-1 break-all rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-sm text-slate-800">
                  {result.enrollmentUrl}
                </code>
                <Button
                  variant="ghost"
                  onClick={() => copyToClipboard(result.enrollmentUrl)}
                  title="Copy URL"
                  className="p-2"
                >
                  <Copy className="h-4 w-4" />
                </Button>
              </div>
            </Field>
          )}

          {copied && <p className="text-sm text-emerald-600">Copied to clipboard!</p>}
        </div>

        <Button
          onClick={() => {
            setResult(null);
            setFirstName("");
            setLastName("");
            setEmail("");
            setRole("");
            setCopied(false);
            onSuccess?.();
          }}
          variant="secondary"
          className="mt-6 w-full"
        >
          Close
        </Button>
      </div>
    );
  }

  const isValid = firstName.trim() !== "" && lastName.trim() !== "" && email.includes("@") && role.trim() !== "";

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <div className="grid grid-cols-2 gap-4">
        <Field label="First Name" required error={fieldErrors.firstName}>
          <Input
            id="firstName"
            value={firstName}
            onChange={(e) => {
              setFirstName(e.target.value);
              if (fieldErrors.firstName) setFieldErrors((prev) => ({ ...prev, firstName: "" }));
            }}
            placeholder="Jane"
          />
        </Field>
        <Field label="Last Name" required error={fieldErrors.lastName}>
          <Input
            id="lastName"
            value={lastName}
            onChange={(e) => {
              setLastName(e.target.value);
              if (fieldErrors.lastName) setFieldErrors((prev) => ({ ...prev, lastName: "" }));
            }}
            placeholder="Doe"
          />
        </Field>
      </div>

      <Field label="Email" required error={fieldErrors.email || emailError}>
        <Input
          id="email"
          type="email"
          value={email}
          onChange={(e) => {
            setEmail(e.target.value);
            if (fieldErrors.email) setFieldErrors((prev) => ({ ...prev, email: "" }));
            setEmailError(e.target.value && !e.target.value.includes("@") ? "Please enter a valid email address" : null);
          }}
          placeholder="jane@example.com"
        />
      </Field>

      <Field label="Department" error={fieldErrors.department}>
        <Select
          id="department"
          value={department}
          onChange={(e) => {
            setDepartment(e.target.value);
            if (fieldErrors.department) setFieldErrors((prev) => ({ ...prev, department: "" }));
          }}
        >
          <option>Engineering</option>
          <option>Marketing</option>
          <option>Sales</option>
          <option>Operations</option>
        </Select>
      </Field>

      <Field label="Role" required error={fieldErrors.role}>
        <Input
          id="role"
          value={role}
          onChange={(e) => {
            setRole(e.target.value);
            if (fieldErrors.role) setFieldErrors((prev) => ({ ...prev, role: "" }));
          }}
          placeholder="e.g. Software Engineer"
        />
      </Field>

      {error && <div className="rounded-lg bg-red-50 p-3 text-sm text-red-700">{error}</div>}

      <Button
        type="submit"
        loading={submitting}
        disabled={!isValid}
        title={!isValid ? "Please fill in all required fields correctly" : ""}
        className="w-full"
      >
        {submitting ? "Onboarding..." : "Submit Onboarding"}
      </Button>
    </form>
  );
}
