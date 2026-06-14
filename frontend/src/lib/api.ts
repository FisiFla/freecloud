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
    firstName?: string;
    lastName?: string;
    email?: string;
    username?: string;
  };
  enrollmentToken: string;
  enrollmentURL: string;
  warning?: string;
  nextStep?: string;
}

export interface OffboardResponse {
  userId: string;
  sessionsTerminated: boolean;
  sessionTerminationError?: string;
  devicesWiped: number;
  devicesFailed: number;
  warnings?: string[];
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
  baseUrl?: string;
  enabled: boolean;
  createdAt?: string;
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
  // The backend returns details as a JSONB object; serialized to a string here
  // for display. Typed as unknown so callers can parse if needed.
  details: Record<string, unknown> | string;
  createdAt: string;
}

export interface Device {
  fleetHostId: string;
  hostname?: string;
  osVersion?: string;
  lastSeenAt?: string;
  createdAt?: string;
}

export interface User {
  id: string;
  keycloakUserId: string;
  email: string;
  firstName: string;
  lastName: string;
  department?: string;
  role?: string;
  createdAt?: string;
  updatedAt?: string;
  devices?: Device[];
}

export interface HealthStatus {
  status: string;
}

// Backend API envelope
interface ApiEnvelope<T> {
  success: boolean;
  data?: T;
  error?: string;
  errors?: { field: string; message: string }[];
}

// ---- Auth token store ----
//
// The access token is populated asynchronously by the SessionProvider once the
// session loads. To avoid the first client-side fetch racing ahead of that
// population (and therefore going out unauthenticated), callers may await
// `waitForAuthToken()` before issuing requests.

let authToken: string | null = null;

export function setAuthToken(token: string | null) {
  authToken = token;
}

export function getAuthToken(): string | null {
  return authToken;
}

// waitForAuthToken resolves once a non-empty token has been published, or after
// a short timeout if none ever arrives (e.g. user is unauthenticated). This lets
// pages block their initial fetch until the auth state is known.
export function waitForAuthToken(timeoutMs = 2000): Promise<string | null> {
  if (authToken) return Promise.resolve(authToken);
  return new Promise((resolve) => {
    const start = Date.now();
    const interval = setInterval(() => {
      if (authToken) {
        clearInterval(interval);
        resolve(authToken);
      } else if (Date.now() - start >= timeoutMs) {
        clearInterval(interval);
        resolve(null);
      }
    }, 50);
  });
}

// ---- Helpers ----

export class ApiError extends Error {
  fieldErrors?: { field: string; message: string }[];
  
  constructor(message: string, fieldErrors?: { field: string; message: string }[]) {
    super(message);
    this.name = "ApiError";
    this.fieldErrors = fieldErrors;
  }
}

function getBaseUrl(): string {
  return process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const url = `${getBaseUrl()}${path}`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (authToken) {
    headers["Authorization"] = `Bearer ${authToken}`;
  }

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
    // If there are field-level errors, throw ApiError with both message and fieldErrors
    if (json.errors && json.errors.length > 0) {
      const messages = json.errors.map((e) => e.message).join("; ");
      throw new ApiError(messages, json.errors);
    }
    throw new ApiError(json.error || `Request failed with status ${res.status}`);
  }

  return json.data as T;
}

// ---- Exported typed functions ----

export async function onboardEmployee(req: OnboardRequest): Promise<OnboardResponse> {
  return request<OnboardResponse>("POST", "/api/v1/onboard", req);
}

export async function offboardUser(userId: string): Promise<OffboardResponse> {
  return request<OffboardResponse>("POST", `/api/v1/offboard/${userId}`);
}

export async function checkDevice(): Promise<DeviceCheckResponse> {
  return request<DeviceCheckResponse>("POST", "/api/v1/auth/device-check", {});
}

export async function createApp(req: CreateAppRequest): Promise<App> {
  return request<App>("POST", "/api/v1/apps/create", req);
}

export async function assignAppToUser(appId: string, userId: string): Promise<void> {
  return request<void>("POST", `/api/v1/apps/${appId}/assign`, { userId });
}

export async function listApps(): Promise<App[]> {
  return request<App[]>("GET", "/api/v1/apps");
}

export async function listUsers(): Promise<User[]> {
  return request<User[]>("GET", "/api/v1/users");
}

export async function getUser(id: string): Promise<User> {
  return request<User>("GET", `/api/v1/users/${id}`);
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

export async function healthKeycloak(): Promise<{status: string}> {
  return request<{status: string}>("GET", "/api/v1/health/keycloak");
}

export async function healthFleet(): Promise<{status: string}> {
  return request<{status: string}>("GET", "/api/v1/health/fleetdm");
}
