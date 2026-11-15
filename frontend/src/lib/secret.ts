// Helpers for registering secrets (see internal/server/secret.go).
// Secret values only ever travel in a POST body; never put them in URLs,
// logs, or anything that is persisted.

/** Same rule as the server's ValidateName. */
export const SECRET_NAME_PATTERN = /^[A-Z_][A-Z0-9_]*$/;

export function isValidSecretName(name: string): boolean {
  return SECRET_NAME_PATTERN.test(name);
}

/** Pending request_secret escalation info (never contains the value). */
export interface SecretRequestInfo {
  name: string;
  description: string;
  projectPath?: string;
}

export type SecretSourceMode = "value" | "1password";

/** Backend chosen for an entered value. "" = server default. */
export type SecretValueBackend = "" | "keychain" | "file";

export interface SecretFormInput {
  name: string;
  description: string;
  mode: SecretSourceMode;
  value: string;
  ref: string;
  backend: SecretValueBackend;
  /** Project path to restrict the secret to; empty = all projects. */
  project: string;
}

/** Body of POST /api/secrets (and of the request-resolve endpoint). */
export interface SecretRegisterBody {
  name: string;
  description: string;
  backend?: string;
  value?: string;
  ref?: string;
  allowedProjects?: string[];
}

export interface SecretMeta {
  name: string;
  description: string;
  backend: string;
  ref?: string;
  allowedProjects?: string[];
  createdAt: string;
  updatedAt: string;
  lastUsedAt?: string | null;
}

export function isValidOnePasswordRef(ref: string): boolean {
  return /^op:\/\/[^/\s]+\/[^/\s]+\/\S+$/.test(ref.trim());
}

/** Returns an error message for the form, or null when it can be submitted. */
export function validateSecretForm(input: SecretFormInput): string | null {
  if (!isValidSecretName(input.name)) return "名前は大文字英数字と _ のみ（先頭は数字以外）で入力してください";
  if (input.mode === "value" && input.value === "") return "値を入力してください";
  if (input.mode === "1password" && !isValidOnePasswordRef(input.ref)) return "op://vault/item/field 形式で入力してください";
  return null;
}

export function buildSecretBody(input: SecretFormInput): SecretRegisterBody {
  const body: SecretRegisterBody = { name: input.name, description: input.description.trim() };
  if (input.mode === "1password") {
    body.backend = "1password";
    body.ref = input.ref.trim();
  } else {
    if (input.backend) body.backend = input.backend;
    body.value = input.value;
  }
  const project = input.project.trim();
  if (project) body.allowedProjects = [project];
  return body;
}

/** Path the notification / banner opens for a request_secret escalation. Only IDs, never values. */
export function secretRequestPath(escalationId: string, sessionId: string): string {
  const q = new URLSearchParams({ escalation: escalationId, session: sessionId });
  return `/secrets/new?${q.toString()}`;
}

export const SECRET_RESPONSE_CANCELLED = "secret_cancelled";
