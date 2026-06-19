"use client";

import { useState } from "react";
import OnboardForm from "./OnboardForm";
import BulkOnboardForm from "./BulkOnboardForm";

type Tab = "single" | "bulk";

export default function OnboardPage() {
  const [tab, setTab] = useState<Tab>("single");

  return (
    <div className="mx-auto max-w-lg">
      <h1 className="text-2xl font-bold text-slate-800">Onboard Employee(s)</h1>
      <p className="mt-1 text-sm text-slate-500">
        Add one employee at a time or import a CSV for bulk onboarding.
      </p>

      {/* Tab switcher */}
      <div className="mt-6 flex rounded-lg border border-slate-200 bg-slate-50 p-1">
        <button
          type="button"
          onClick={() => setTab("single")}
          className={`flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
            tab === "single"
              ? "bg-white text-slate-800 shadow-sm"
              : "text-slate-500 hover:text-slate-700"
          }`}
        >
          Single Employee
        </button>
        <button
          type="button"
          onClick={() => setTab("bulk")}
          className={`flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
            tab === "bulk"
              ? "bg-white text-slate-800 shadow-sm"
              : "text-slate-500 hover:text-slate-700"
          }`}
        >
          Bulk CSV Import
        </button>
      </div>

      <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        {tab === "single" ? (
          <OnboardForm />
        ) : (
          <BulkOnboardForm />
        )}
      </div>
    </div>
  );
}
