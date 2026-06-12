"use client";

import { useEffect, useState } from "react";
import { CheckCircle, XCircle, RefreshCw, AlertCircle } from "lucide-react";
import { healthCheck } from "@/lib/api";

interface ConnectionStatus {
  label: string;
  endpoint: string;
  status: "unknown" | "connected" | "disconnected";
  checking: boolean;
}

export default function SettingsPage() {
  const [healthStatus, setHealthStatus] = useState<string | null>(null);
  const [healthLoading, setHealthLoading] = useState(true);
  const [healthError, setHealthError] = useState<string | null>(null);

  const [services, setServices] = useState<ConnectionStatus[]>([
    { label: "Backend API", endpoint: "/api/v1/health", status: "unknown", checking: false },
    { label: "Keycloak Connection", endpoint: "/api/v1/health/keycloak", status: "unknown", checking: false },
    { label: "FleetDM Connection", endpoint: "/api/v1/health/fleetdm", status: "unknown", checking: false },
  ]);

  useEffect(() => {
    const fetchHealth = async () => {
      try {
        setHealthLoading(true);
        setHealthError(null);
        const result = await healthCheck();
        setHealthStatus(result.status);
      } catch (err: unknown) {
        setHealthError(err instanceof Error ? err.message : "Health check failed");
      } finally {
        setHealthLoading(false);
      }
    };
    fetchHealth();
  }, []);

  const testConnection = async (index: number) => {
    setServices((prev) =>
      prev.map((s, i) => (i === index ? { ...s, checking: true, status: "unknown" } : s))
    );

    try {
      const res = await fetch(`http://localhost:8080${services[index].endpoint}`);
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

      {/* Health check status */}
      <div className="mt-6 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        <h2 className="text-lg font-semibold text-slate-800">System Health</h2>
        {healthLoading ? (
          <div className="mt-3 h-6 w-48 animate-pulse rounded bg-slate-200" />
        ) : healthError ? (
          <div className="mt-3 flex items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
            <AlertCircle className="h-5 w-5 shrink-0" />
            <span>{healthError}</span>
          </div>
        ) : (
          <div className="mt-3 flex items-center gap-2">
            <span
              className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
                healthStatus === "OK"
                  ? "bg-emerald-50 text-emerald-700"
                  : "bg-red-50 text-red-700"
              }`}
            >
              {healthStatus === "OK" ? "Healthy" : healthStatus || "Unknown"}
            </span>
            <span className="text-sm text-slate-500">
              Backend status: {healthStatus || "unknown"}
            </span>
          </div>
        )}
      </div>

      <div className="mt-6 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
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
