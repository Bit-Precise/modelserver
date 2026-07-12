import type { ErrorResponse } from "./types";

// API base URL — empty string means same-origin (relative paths).
// Set VITE_API_BASE_URL to point to a different domain, e.g. "https://api.cs.ac.cn".
export const API_BASE = (import.meta.env.VITE_API_BASE_URL as string) || "";

let accessToken: string | null = null;
let refreshToken: string | null = null;
let refreshPromise: Promise<boolean> | null = null;

export function setTokens(access: string, refresh: string) {
  accessToken = access;
  refreshToken = refresh;
  localStorage.setItem("refresh_token", refresh);
}

export function clearTokens() {
  accessToken = null;
  refreshToken = null;
  localStorage.removeItem("refresh_token");
}

export function getAccessToken() {
  return accessToken;
}

export function getStoredRefreshToken() {
  return refreshToken || localStorage.getItem("refresh_token");
}

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message);
    this.name = "APIError";
  }
}

async function tryRefresh(): Promise<boolean> {
  const rt = getStoredRefreshToken();
  if (!rt) return false;

  try {
    const res = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: rt }),
    });
    if (!res.ok) {
      clearTokens();
      return false;
    }
    const data = await res.json();
    setTokens(data.access_token, data.refresh_token);
    return true;
  } catch {
    clearTokens();
    return false;
  }
}

/**
 * Fetch transport shared by the legacy request helpers and the generated
 * OpenAPI client. It owns the dashboard's bearer token and single-flight
 * refresh behavior so both clients have identical authentication semantics.
 */
export async function authenticatedFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const originalRequest = new Request(input, init);
  const headers = new Headers(originalRequest.headers);
  if (accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`);
  }

  // Keep an untouched request available for a single replay after refresh.
  let request = new Request(originalRequest, { headers });
  let res = await fetch(request.clone());

  if (res.status === 401 && getStoredRefreshToken()) {
    if (!refreshPromise) {
      refreshPromise = tryRefresh().finally(() => {
        refreshPromise = null;
      });
    }
    const refreshed = await refreshPromise;
    if (refreshed) {
      const retryHeaders = new Headers(request.headers);
      retryHeaders.set("Authorization", `Bearer ${accessToken}`);
      request = new Request(request, { headers: retryHeaders });
      res = await fetch(request);
    }
  }

  return res;
}

function isErrorResponse(value: unknown): value is ErrorResponse {
  if (!value || typeof value !== "object" || !("error" in value)) {
    return false;
  }
  const error = value.error;
  return (
    !!error &&
    typeof error === "object" &&
    "code" in error &&
    typeof error.code === "string" &&
    "message" in error &&
    typeof error.message === "string"
  );
}

/** Convert a management API error envelope into the dashboard's public error type. */
export function toAPIError(
  response: Response,
  body: unknown,
): APIError {
  const errBody = isErrorResponse(body) ? body : undefined;
  return new APIError(
    response.status,
    errBody?.error.code ?? "unknown",
    errBody?.error.message ?? response.statusText,
    errBody?.error.details,
  );
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers);
  if (!headers.has("Content-Type") && options.body) {
    headers.set("Content-Type", "application/json");
  }
  const res = await authenticatedFetch(`${API_BASE}${path}`, {
    ...options,
    headers,
  });

  if (!res.ok) {
    let errBody: unknown;
    try {
      errBody = await res.json();
    } catch {
      // ignore parse errors
    }
    throw toAPIError(res, errBody);
  }

  if (res.status === 204 || res.status === 201) {
    // 201 Created and 204 No Content may have no body
    const text = await res.text();
    if (!text) return undefined as T;
    return JSON.parse(text);
  }
  return res.json();
}

export const api = {
  get<T>(path: string) {
    return request<T>(path);
  },
  post<T>(path: string, body?: unknown) {
    return request<T>(path, {
      method: "POST",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  put<T>(path: string, body?: unknown) {
    return request<T>(path, {
      method: "PUT",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  patch<T>(path: string, body?: unknown) {
    return request<T>(path, {
      method: "PATCH",
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  delete<T>(path: string) {
    return request<T>(path, { method: "DELETE" });
  },
};
