"use client";

import OnboardForm from "./OnboardForm";

export default function OnboardPage() {
  return (
    <div className="mx-auto max-w-lg">
      <h1 className="text-2xl font-bold text-slate-800">Onboard New Employee</h1>
      <p className="mt-1 text-sm text-slate-500">
        Fill in the details to create a new employee account.
      </p>

      <div className="mt-8 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        <OnboardForm />
      </div>
    </div>
  );
}
