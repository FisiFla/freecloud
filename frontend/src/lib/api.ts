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
export interface CreateAppResponse {
  id: string;
  name: string;
  keycloakClientId: string;
  // SAML SP metadata — populated when protocol === "SAML"
  samlEntityId?: string;
  samlAcsUrl?: string;
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

// A3 — Groups & Roles
export interface Group {
  id: string;
  name: string;
}

export interface RealmRole {
  id: string;
  name: string;
}

// A4 — User profile update
export interface PatchUserRequest {
  firstName?: string;
  lastName?: string;
  department?: string;
  role?: string;
  enabled?: boolean;
}

export interface AuditLogFilters {
  actor?: string;
  action?: string;
  limit?: number;
  offset?: number;
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
  disabled?: boolean;
  createdAt?: string;
  updatedAt?: string;
  devices?: Device[];
}

export interface HealthStatus {
  status: string;
}

// C1: Bulk onboarding
export interface BulkOnboardRowResult {
  row: number;
  email: string;
  status: "succeeded" | "skipped-duplicate" | "failed";
  reason?: string;
}

export interface BulkOnboardResponse {
  total: number;
  succeeded: number;
  skipped: number;
  failed: number;
  results: BulkOnboardRowResult[];
}

// C2: MFA status
export interface MFAStatus {
  userId: string;
  otpEnabled: boolean;
  otpPending: boolean;
  webAuthnEnabled: boolean;
}

// B1
export interface RemoteLockResponse {
  deviceId: string;
  locked: boolean;
}

// B2
export interface SoftwareEntry {
  name: string;
  version: string;
  vulnerabilities: string[];
}

export interface DeviceSoftwareHost {
  deviceId: string;
  hostname?: string;
  software: SoftwareEntry[];
}

export interface DeviceSoftwareResponse {
  userId: string;
  devices: DeviceSoftwareHost[];
}

// B3
export interface DeviceHostPosture {
  deviceId: string;
  hostname?: string;
  osVersion?: string;
  diskEncrypted: boolean;
  firewallEnabled: boolean;
  mdmEnrolled: boolean;
  vulnerabilities: string[];
  unknownVulns: boolean;
  compliant: boolean;
}

export interface ComplianceSummary {
  totalDevices: number;
  compliantDevices: number;
  encryptedDevices: number;
  firewallEnabled: number;
  devicesWithVulns: number;
}

export interface ComplianceResponse {
  userId?: string;
  summary: ComplianceSummary;
  devices: DeviceHostPosture[];
}

// B4 (kept for global policy listing)
export interface Policy {
  id: string;
  name: string;
  query?: string;
  description?: string;
  resolution?: string;
  teamId?: string;
}

// B2: Fleet teams
export interface FleetTeam {
  id: number;
  name: string;
  description?: string;
}

export interface AssignTeamPolicyResponse {
  teamId: number;
  policyId: string;
  assigned: boolean;
}

export interface MoveHostResponse {
  teamId: number;
  moved: number;
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
// The access token is published here by the SessionProvider (via setAuthToken)
// and attached to outgoing requests by the request() helper below. Components
// should gate fetches on the `useApiReady()` hook from app/providers rather
// than polling this store directly.

let authToken: string | null = null;

export function setAuthToken(token: string | null) {
  // Only ever store the token in the browser. This module has no "use client"
  // directive, so guard against a future server-side import accumulating a
  // process-wide token that would leak across concurrent requests.
  if (typeof window === "undefined") return;
  authToken = token;
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
  const url = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
  // In production the API must be reached over TLS or bearer tokens travel in
  // plaintext. Fail closed on the server; loudly warn in the browser (the value
  // is already baked into the bundle at build time and can't be changed here).
  if (process.env.NODE_ENV === "production" && url.startsWith("http://")) {
    const msg = `NEXT_PUBLIC_API_URL must use https:// in production (got: ${url})`;
    if (typeof window === "undefined") throw new Error(msg);
    // eslint-disable-next-line no-console
    console.error(msg);
  }
  return url;
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
  if (filters?.offset) params.set("offset", String(filters.offset));
  const qs = params.toString();
  return request<AuditLogEntry[]>("GET", `/api/v1/audit-logs${qs ? `?${qs}` : ""}`);
}

// C1: Bulk CSV onboarding
export async function bulkOnboardEmployees(
  file: File,
): Promise<BulkOnboardResponse> {
  const form = new FormData();
  form.append("file", file);
  const url = `${getBaseUrl()}/api/v1/onboard/bulk`;
  const headers: Record<string, string> = {};
  if (authToken) headers["Authorization"] = `Bearer ${authToken}`;
  const res = await fetch(url, { method: "POST", headers, body: form });
  const text = await res.text();
  let json: ApiEnvelope<BulkOnboardResponse>;
  try {
    json = JSON.parse(text);
  } catch {
    throw new ApiError(`HTTP ${res.status}: ${text || res.statusText}`);
  }
  if (!json.success && res.status !== 207) {
    throw new ApiError(json.error || `Request failed with status ${res.status}`);
  }
  return json.data as BulkOnboardResponse;
}

// C2: MFA status
export async function getMFAStatus(userId: string): Promise<MFAStatus> {
  return request<MFAStatus>("GET", `/api/v1/users/${userId}/mfa-status`);
}

export async function requireMFA(
  userId: string,
  type: "totp" | "webauthn",
): Promise<{ userId: string; action: string; set: boolean }> {
  return request("POST", `/api/v1/users/${userId}/require-mfa`, { type });
}

// C3: Forgot password (public — no auth token needed, but we pass one if present)
export async function forgotPassword(email: string): Promise<{ message: string }> {
  return request<{ message: string }>("POST", "/api/v1/auth/forgot-password", { email });
}

// C4: Audit log export — opens a browser download
export function downloadAuditLogs(
  format: "csv" | "json",
  filters?: { actor?: string; action?: string },
): void {
  const params = new URLSearchParams({ format });
  if (filters?.actor) params.set("actor", filters.actor);
  if (filters?.action) params.set("action", filters.action);
  const base = getBaseUrl();
  const url = `${base}/api/v1/audit-logs/export?${params.toString()}`;
  // Open in current tab — browser will treat it as a download due to
  // Content-Disposition: attachment on the backend response.
  window.location.href = url;
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

// A3 — Group & role management
export async function listGroups(): Promise<Group[]> {
  return request<Group[]>("GET", "/api/v1/groups");
}

export async function createGroup(name: string): Promise<Group> {
  return request<Group>("POST", "/api/v1/groups", { name });
}

export async function assignUserToGroup(userId: string, groupId: string): Promise<void> {
  return request<void>("POST", `/api/v1/users/${userId}/groups`, { groupId });
}

export async function unassignUserFromGroup(userId: string, groupId: string): Promise<void> {
  return request<void>("DELETE", `/api/v1/users/${userId}/groups/${groupId}`);
}

export async function listRealmRoles(): Promise<RealmRole[]> {
  return request<RealmRole[]>("GET", "/api/v1/roles");
}

export async function assignRoleToUser(userId: string, roleId: string, roleName: string): Promise<void> {
  return request<void>("POST", `/api/v1/users/${userId}/roles`, { roleId, roleName });
}

// A4 — User profile update
export async function patchUser(userId: string, req: PatchUserRequest): Promise<User> {
  return request<User>("PATCH", `/api/v1/users/${userId}`, req);
}

// A5 — Password reset
export async function resetPassword(userId: string): Promise<{ sent: boolean }> {
  return request<{ sent: boolean }>("POST", `/api/v1/users/${userId}/reset-password`);
}

// B1: Remote lock
export async function lockDevice(deviceId: string): Promise<RemoteLockResponse> {
  return request<RemoteLockResponse>("POST", `/api/v1/devices/${deviceId}/lock`);
}

// B2: Software inventory for a user's devices
export async function getUserDeviceSoftware(userId: string): Promise<DeviceSoftwareResponse> {
  return request<DeviceSoftwareResponse>("GET", `/api/v1/users/${userId}/devices/software`);
}

// B3: Compliance posture
export async function getUserCompliance(userId: string): Promise<ComplianceResponse> {
  return request<ComplianceResponse>("GET", `/api/v1/users/${userId}/devices/compliance`);
}

export async function getOrgCompliance(): Promise<ComplianceResponse> {
  return request<ComplianceResponse>("GET", `/api/v1/compliance`);
}

// D2: Analytics snapshots
export interface SnapshotRow {
  id: number;
  capturedAt: string;
  complianceRate: number;
  enrolledDevices: number;
  mfaCoveragePct: number;
  appCount: number;
  onboardCount: number;
  offboardCount: number;
}

export async function getAnalyticsSnapshots(limit?: number): Promise<SnapshotRow[]> {
  const qs = limit ? `?limit=${limit}` : "";
  return request<SnapshotRow[]>("GET", `/api/v1/analytics/snapshots${qs}`);
}

// B4 / B2: Policies
export async function listPolicies(): Promise<{ policies: Policy[] }> {
  return request<{ policies: Policy[] }>("GET", "/api/v1/policies");
}

// B2: Fleet team management
export async function listTeams(): Promise<{ teams: FleetTeam[] }> {
  return request<{ teams: FleetTeam[] }>("GET", "/api/v1/teams");
}

export async function createTeam(name: string, description?: string): Promise<FleetTeam> {
  return request<FleetTeam>("POST", "/api/v1/teams", { name, description });
}

export async function assignPolicyToTeam(teamId: number, policyId: string): Promise<AssignTeamPolicyResponse> {
  return request<AssignTeamPolicyResponse>("POST", `/api/v1/teams/${teamId}/policies`, { policyId });
}

export async function moveHostToTeam(teamId: number, hostIds: string[]): Promise<MoveHostResponse> {
  return request<MoveHostResponse>("POST", `/api/v1/teams/${teamId}/hosts`, { hostIds });
}

// A3: per-app access policy (conditional access)
export interface AppAccessPolicy {
  appId: string;
  requireEnrolled: boolean;
  requireDiskEncrypted: boolean;
  requireNoCriticalVulns: boolean;
  maxOsAgeDays?: number;
  updatedAt?: string;
}

export async function getAppPolicy(appId: string): Promise<AppAccessPolicy> {
  return request<AppAccessPolicy>("GET", `/api/v1/apps/${appId}/policy`);
}

export async function upsertAppPolicy(
  appId: string,
  policy: Omit<AppAccessPolicy, "appId" | "updatedAt">,
): Promise<AppAccessPolicy> {
  return request<AppAccessPolicy>("PUT", `/api/v1/apps/${appId}/policy`, {
    appId,
    ...policy,
  });
}

// C2 (Epic C): API token management
export interface APIToken {
  id: string;
  name: string;
  token?: string; // present only on creation
  role: string;
  serviceIdentity: string;
  createdAt: string;
  expiresAt?: string;
}

export interface CreateAPITokenRequest {
  name: string;
  role: string;
  serviceIdentity: string;
  expiresInDays: number; // 0 = never
}

export async function listAPITokens(): Promise<APIToken[]> {
  const res = await request<{ tokens: APIToken[] }>("GET", "/api/v1/api-tokens");
  return res.tokens;
}

export async function createAPIToken(req: CreateAPITokenRequest): Promise<APIToken> {
  return request<APIToken>("POST", "/api/v1/api-tokens", req);
}

export async function revokeAPIToken(id: string): Promise<void> {
  return request<void>("DELETE", `/api/v1/api-tokens/${id}`);
}

// C4 (Epic C): Self-service portal
export interface PortalDevice {
  fleetHostId: string;
  hostname: string;
  osVersion: string;
  lastSeenAt?: string;
  createdAt: string;
}

export interface PortalApp {
  id: string;
  name: string;
  baseUrl: string;
  protocol: string;
  enabled: boolean;
}

export interface AccessRequest {
  id: string;
  requesterId: string;
  appId: string;
  status: string;
  reason: string;
  decidedBy: string;
  createdAt: string;
}

export async function portalMyDevices(): Promise<PortalDevice[]> {
  return request<PortalDevice[]>("GET", "/api/v1/portal/me/devices");
}

export async function portalMyApps(): Promise<PortalApp[]> {
  return request<PortalApp[]>("GET", "/api/v1/portal/me/apps");
}

export async function portalMyCompliance(): Promise<ComplianceResponse> {
  return request<ComplianceResponse>("GET", "/api/v1/portal/me/compliance");
}

export async function portalRequestAccess(appId: string, reason: string): Promise<{ id: string }> {
  return request<{ id: string }>("POST", "/api/v1/portal/access-requests", { appId, reason });
}

export async function listAccessRequests(): Promise<AccessRequest[]> {
  return request<AccessRequest[]>("GET", "/api/v1/portal/access-requests");
}

export async function decideAccessRequest(
  id: string,
  decision: "approved" | "rejected",
): Promise<{ status: string }> {
  return request<{ status: string }>("PATCH", `/api/v1/portal/access-requests/${id}`, { decision });
}
