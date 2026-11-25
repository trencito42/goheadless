// Thin fetch wrapper for the CMS management API. Session auth is cookie
// based, so every request sends credentials; there is no token to attach.

export class ApiError extends Error {
  status: number;
  constructor(status: number, detail: string) {
    super(detail);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
  if (res.status === 204) {
    return undefined as T;
  }
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    throw new ApiError(res.status, body?.detail ?? res.statusText);
  }
  return body as T;
}

export interface User {
  id: string;
  email: string;
  isOwner: boolean;
}

export interface Project {
  id: string;
  name: string;
  slug: string;
  createdAt: string;
  updatedAt: string;
}

export type FieldType =
  | "short_text"
  | "long_text"
  | "rich_text"
  | "integer"
  | "decimal"
  | "boolean"
  | "email"
  | "url"
  | "date"
  | "datetime"
  | "enumeration"
  | "json";

export interface SchemaField {
  name: string;
  type: FieldType;
  required?: boolean;
  unique?: boolean;
  enum?: string[];
}

export interface ContentType {
  id: string;
  projectId: string;
  apiId: string;
  displayName: string;
  kind: "collection" | "single";
  schema: { fields: SchemaField[] };
  schemaVersion: number;
}

export interface Entry {
  id: string;
  projectId: string;
  contentTypeId: string;
  documentId: string;
  locale: string;
  status: "draft" | "published";
  data: Record<string, unknown>;
  version: number;
  createdAt: string;
  updatedAt: string;
}

export const api = {
  login: (email: string, password: string) =>
    request<User>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),
  me: () => request<User>("/api/v1/auth/me"),

  listProjects: () => request<Project[]>("/api/v1/manage/projects/"),
  createProject: (name: string, slug: string) =>
    request<Project>("/api/v1/manage/projects/", { method: "POST", body: JSON.stringify({ name, slug }) }),

  listContentTypes: (projectId: string) =>
    request<ContentType[]>(`/api/v1/manage/projects/${projectId}/content-types/`),
  createContentType: (
    projectId: string,
    body: { apiId: string; displayName: string; kind: "collection" | "single"; schema: { fields: SchemaField[] } },
  ) =>
    request<ContentType>(`/api/v1/manage/projects/${projectId}/content-types/`, {
      method: "POST",
      body: JSON.stringify(body),
    }),

  listEntries: (projectId: string, apiId: string) =>
    request<{ data: Entry[] }>(`/api/v1/manage/projects/${projectId}/content/${apiId}/`),
  createEntry: (projectId: string, apiId: string, data: Record<string, unknown>) =>
    request<Entry>(`/api/v1/manage/projects/${projectId}/content/${apiId}/`, {
      method: "POST",
      body: JSON.stringify({ data }),
    }),
  publishEntry: (projectId: string, apiId: string, documentId: string) =>
    request<Entry>(`/api/v1/manage/projects/${projectId}/content/${apiId}/${documentId}/publish`, {
      method: "POST",
    }),
};
