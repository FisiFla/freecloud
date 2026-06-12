// ---------------------------------------------------------------------------
// FreeCloud API client — types + typed fetch wrapper
// ---------------------------------------------------------------------------

// ---- Type definitions matching backend Go structs ----

export interface OnboardRequest {
  firstName: string;
  lastName: string;
  email: string;
  department: string;
  role: string;
}

export interface OnboardResponse {
  user: {
    id: string;
    username: string;
    email: string;
  };
  enrollmentToken: string;
  enrollmentURL: string;
  warning?: string;
}

export interface DeviceCheckResponse {
  passed: boolean;
  failures?: { type: string; detail: string }[];
}

export interface CreateAppRequest {
  name: string;
  protocol: "OIDC" | "SAML";
  redirectURIs: string[];
  baseURL: string;
}

export interface App {
  id: string;
  keycloakClientId: string;
  name: string;
  protocol: string;
  baseURL: string;
  enabled: boolean;
  createdAt: string;
}

export interface AuditLogFilters {
  actor?: string;
  action?: string;
  limit?: number;
}

export interface AuditLogEntry {
  id: string;
  actorId: string;
  action: string;
  targetType: string;
  targetId: string;
  details: string;
  createdAt: string;
}

export interface HealthStatus {
  status: string;
}

// Backend API envelope
interface ApiEnvelope<T> {
  success: boolean;
  data?: T;
  error?: string;
}

// ---- Helpers ----

function getBaseUrl(): string {
  return process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
}

function getActorId(): string {
  if (typeof window === "undefined") return "anonymous";
  return localStorage.getItem("actorId") || "anonymous";
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const url = `${getBaseUrl()}${path}`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Actor-ID": getActorId(),
  };

  const res = await fetch(url, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  // Handle non-JSON responses (e.g. empty 200)
  const text = await res.text();
  let json: ApiEnvelope<T>;
  try {
    json = JSON.parse(text);
  } catch {
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${text || res.statusText}`);
    // If it's a 2xx with no JSON body, treat as success with void data
    return undefined as T;
  }

  if (!json.success) {
    throw new Error(json.error || `Request failed with status ${res.status}`);
  }

  return json.data as T;
}

// ---- Exported typed functions ----

export async function onboardEmployee(req: OnboardRequest): Promise<OnboardResponse> {
  return request<OnboardResponse>("POST", "/api/v1/onboard", req);
}

export async function offboardUser(userId: string): Promise<void> {
  return request<void>("POST", `/api/v1/offboard/${userId}`);
}

export async function checkDevice(userId: string): Promise<DeviceCheckResponse> {
  return request<DeviceCheckResponse>("GET", `/api/v1/devices/check/${userId}`);
}

export async function createApp(req: CreateAppRequest): Promise<App> {
  return request<App>("POST", "/api/v1/apps/create", req);
}

export async function assignAppToUser(appId: string, userId: string): Promise<void> {
  return request<void>("POST", "/api/v1/apps/assign", { appId, userId });
}

export async function listApps(): Promise<App[]> {
  return request<App[]>("GET", "/api/v1/apps");
}

export async function listAuditLogs(filters?: AuditLogFilters): Promise<AuditLogEntry[]> {
  const params = new URLSearchParams();
  if (filters?.actor) params.set("actor", filters.actor);
  if (filters?.action) params.set("action", filters.action);
  if (filters?.limit) params.set("limit", String(filters.limit));
  const qs = params.toString();
  return request<AuditLogEntry[]>("GET", `/api/v1/audit-logs${qs ? `?${qs}` : ""}`);
}

export async function healthCheck(): Promise<HealthStatus> {
  return request<HealthStatus>("GET", "/api/v1/health");
}
