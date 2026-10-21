import type { SessionInfo } from "./types";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

let csrf = sessionStorage.getItem("mesh.csrf") ?? "";

export function setCSRF(token: string) {
  csrf = token;
  if (token) {
    sessionStorage.setItem("mesh.csrf", token);
  } else {
    sessionStorage.removeItem("mesh.csrf");
  }
}

type RequestOptions = {
  method?: Method;
  body?: unknown;
  signal?: AbortSignal;
};

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, signal } = options;
  const headers = new Headers();
  if (body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  if (method !== "GET" && csrf) {
    headers.set("X-CSRF-Token", csrf);
  }
  const response = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });

  if (response.status === 401) {
    setCSRF("");
    window.dispatchEvent(new Event("mesh:unauthorized"));
  }

  if (!response.ok) {
    let code = "HTTP_ERROR";
    let message = response.statusText;
    const contentType = response.headers.get("content-type") ?? "";
    if (contentType.includes("application/json")) {
      try {
        const parsed = (await response.json()) as {
          error?: { code?: string; message?: string };
        };
        code = parsed.error?.code ?? code;
        message = parsed.error?.message ?? message;
      } catch {
        // keep the status text
      }
    }
    throw new ApiError(response.status, code, message || `HTTP ${response.status}`);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

export async function login(username: string, password: string): Promise<SessionInfo> {
  const session = await api.post<SessionInfo>("/api/auth/login", { username, password });
  setCSRF(session.csrf_token);
  return session;
}

export async function fetchSession(): Promise<SessionInfo> {
  const session = await api.get<SessionInfo>("/api/auth/session");
  setCSRF(session.csrf_token);
  return session;
}

export async function logout(): Promise<void> {
  try {
    await api.post<void>("/api/auth/logout");
  } finally {
    setCSRF("");
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}
