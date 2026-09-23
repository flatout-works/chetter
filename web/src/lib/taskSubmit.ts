// Helpers for the task submission form: timeout presets, client-side
// validation, and the SubmitTask payload. Kept free of Svelte runes so the
// timeout/payload behavior is directly unit-testable.

// DEFAULT_TASK_TIMEOUT_PRESET is the sentinel select value for "use the
// server default"; it resolves to timeoutSec 0, which the server replaces
// with DEFAULT_TASK_TIMEOUT_SEC.
export const DEFAULT_TASK_TIMEOUT_PRESET = "";
// CUSTOM_TASK_TIMEOUT_PRESET reveals the free-form numeric input.
export const CUSTOM_TASK_TIMEOUT_PRESET = "custom";

// The server rejects negative timeouts and caps them at 24h by default
// (internal/validation.DefaultTaskLimits). Mirror that for client-side
// validation so the user gets an immediate message instead of a round trip.
export const MIN_TASK_TIMEOUT_SEC = 1;
export const MAX_TASK_TIMEOUT_SEC = 24 * 60 * 60;

export interface TimeoutPreset {
  label: string;
  /** "" = server default, "custom" = free-form, otherwise seconds as a string. */
  value: string;
}

export const TIMEOUT_PRESETS: TimeoutPreset[] = [
  { label: "15 minutes", value: String(15 * 60) },
  { label: "30 minutes", value: String(30 * 60) },
  { label: "1 hour", value: String(60 * 60) },
  { label: "2 hours", value: String(2 * 60 * 60) },
  { label: "6 hours", value: String(6 * 60 * 60) },
  { label: "12 hours", value: String(12 * 60 * 60) },
  { label: "24 hours", value: String(24 * 60 * 60) },
  { label: "Custom…", value: CUSTOM_TASK_TIMEOUT_PRESET },
];

/**
 * Resolves the selected preset plus optional custom value to seconds.
 * Returns 0 for the server default. Returns NaN when a custom value is not
 * a number, so callers can surface a validation error.
 */
export function resolveTimeoutSec(preset: string, custom: string | number): number {
  if (preset === DEFAULT_TASK_TIMEOUT_PRESET) return 0;
  const raw = preset === CUSTOM_TASK_TIMEOUT_PRESET ? custom : preset;
  if (typeof raw === "string" && raw.trim() === "") return Number.NaN;
  const parsed = typeof raw === "number" ? raw : Number(String(raw).trim());
  if (!Number.isFinite(parsed)) return Number.NaN;
  return Math.trunc(parsed);
}

/**
 * Validates a resolved timeout in seconds. Returns a user-facing message or
 * null when valid.
 */
export function validateTimeoutSec(sec: number): string | null {
  if (!Number.isFinite(sec) || !Number.isInteger(sec)) {
    return "Enter a whole number of seconds.";
  }
  if (sec < MIN_TASK_TIMEOUT_SEC) {
    return `Timeout must be at least ${MIN_TASK_TIMEOUT_SEC} second.`;
  }
  if (sec > MAX_TASK_TIMEOUT_SEC) {
    return "Timeout must be at most 24 hours (86400 seconds).";
  }
  return null;
}

/** Formats seconds as a compact human-readable duration, e.g. 600 -> "10m". */
export function formatTimeoutSec(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "";
  if (sec % 3600 === 0) return `${sec / 3600}h`;
  if (sec % 60 === 0) return `${sec / 60}m`;
  return `${sec}s`;
}

export interface TaskSubmitFormState {
  prompt: string;
  gitUrl: string;
  gitRef: string;
  agentImage: string;
  agent: string;
  providerId: string;
  modelId: string;
  variantId: string;
  harness: string;
  sessionMode: string;
  pauseReason: string;
  ttlHours: number;
  timeoutSec: number;
}

export interface TaskSubmitPayload {
  prompt: string;
  gitUrl: string;
  gitRef: string;
  agentImage: string;
  agent: string;
  providerId: string;
  modelId: string;
  variantId: string;
  harness: string;
  sessionMode: string;
  pauseReason: string;
  ttlHours: number;
  timeoutSec: number;
}

/**
 * Builds the SubmitTask payload from the form state. This is the single
 * source of truth for what the UI submits, including the pre-creation
 * timeout (0 lets the server default apply).
 */
export function buildTaskSubmitPayload(form: TaskSubmitFormState): TaskSubmitPayload {
  return {
    prompt: form.prompt.trim(),
    gitUrl: form.gitUrl.trim(),
    gitRef: form.gitRef.trim(),
    agentImage: form.agentImage.trim(),
    agent: form.agent.trim(),
    providerId: form.providerId.trim(),
    modelId: form.modelId.trim(),
    variantId: form.variantId.trim(),
    harness: form.harness.trim(),
    sessionMode: form.sessionMode || "",
    pauseReason: form.sessionMode === "resumable" ? form.pauseReason.trim() || "" : "",
    ttlHours: form.sessionMode === "resumable" ? form.ttlHours : 0,
    timeoutSec: form.timeoutSec,
  };
}
