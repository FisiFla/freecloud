"use client";

import { useState } from "react";
import { CheckCircle, XCircle, RefreshCw } from "lucide-react";

interface ConnectionStatus {
  label: string;
  endpoint: string;
  status: "unknown" | "connected" | "disconnected";
  checking: boolean;
}

export default function SettingsPage() {
  const [services, setServices] = useState<ConnectionStatus[]>([
    { label: "Backend API", endpoint: "http://localhost:8080/api/v1/health", status: "unknown", checking: false },
    { label: "Keycloak Connection", endpoint: "http://localhost:8080/api/v1/health/keycloak", status: "unknown", checking: false },
    { label: "FleetDM Connection", endpoint: "http://localhost:8080/api/v1/health/fleetdm", status: "unknown", checking: false },
  ]);

  const testConnection = async (index: number) => {
    setServices((prev) =>
      prev.map((s, i) => (i === index ? { ...s, checking: true, status: "unknown" } : s))
    );

    try {
      const res = await fetch(services[index].endpoint);
      const connected = res.ok;
      setServices((prev) =>
        prev.map((s, i) =>
          i === index ? { ...s, checking: false, status: connected ? "connected" : "disconnected" } : s
        )
      );
    } catch {
      setServices((prev) =>
        prev.map((s, i) =>
          i === index ? { ...s, checking: false, status: "disconnected" } : s
        )
      );
    }
  };

  return (
    <div>
      <h1 className="text-2xl font-bold text-slate-800">Settings</h1>
      <p className="mt-1 text-sm text-slate-500">View connection status and system health.</p>

      <div className="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
        {services.map((service, index) => (
          <div
            key={service.label}
            className="rounded-xl border border-slate-200 bg-white p-6 shadow-sm"
          >
            <div className="flex items-center justify-between">
              <h3 className="font-semibold text-slate-800">{service.label}</h3>
              <div className="flex items-center gap-2">
                {service.checking ? (
                  <RefreshCw className="h-5 w-5 animate-spin text-slate-400" />
                ) : service.status === "connected" ? (
                  <CheckCircle className="h-5 w-5 text-emerald-500" />
                ) : service.status === "disconnected" ? (
                  <XCircle className="h-5 w-5 text-red-500" />
                ) : (
                  <div className="h-5 w-5 rounded-full bg-slate-200" />
                )}
              </div>
            </div>

            <p className="mt-2 text-xs text-slate-400 truncate">{service.endpoint}</p>

            <div className="mt-4 flex items-center gap-2">
              <span
                className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                  service.status === "connected"
                    ? "bg-emerald-50 text-emerald-700"
                    : service.status === "disconnected"
                    ? "bg-red-50 text-red-700"
                    : "bg-slate-100 text-slate-500"
                }`}
              >
                {service.checking
                  ? "Checking..."
                  : service.status === "connected"
                  ? "Connected"
                  : service.status === "disconnected"
                  ? "Disconnected"
                  : "Not Tested"}
              </span>
            </div>

            <button
              onClick={() => testConnection(index)}
              disabled={service.checking}
              className="mt-4 w-full rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {service.checking ? "Testing..." : "Test Connection"}
            </button>
          </div>
        ))}
      </div>

      {/* Config Info */}
      <div className="mt-8 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        <h2 className="text-lg font-semibold text-slate-800">API Configuration</h2>
        <p className="mt-1 text-sm text-slate-500">
          The frontend connects to the backend at{" "}
          <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs text-slate-700">
            {process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080"}
          </code>
        </p>
      </div>
    </div>
  );
}
