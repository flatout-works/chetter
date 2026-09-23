import { describe, expect, it } from "vitest";
import {
  buildTaskSubmitPayload,
  resolveTimeoutSec,
  validateTimeoutSec,
  formatTimeoutSec,
  DEFAULT_TASK_TIMEOUT_PRESET,
  CUSTOM_TASK_TIMEOUT_PRESET,
  MAX_TASK_TIMEOUT_SEC,
  type TaskSubmitFormState,
} from "./taskSubmit";

function baseForm(overrides: Partial<TaskSubmitFormState> = {}): TaskSubmitFormState {
  return {
    prompt: "do the thing",
    gitUrl: "",
    gitRef: "",
    agentImage: "",
    agent: "",
    providerId: "openai",
    modelId: "gpt",
    variantId: "",
    harness: "opencode",
    sessionMode: "",
    pauseReason: "",
    ttlHours: 72,
    timeoutSec: 0,
    ...overrides,
  };
}

describe("resolveTimeoutSec", () => {
  it("resolves the default preset to 0 so the server default applies", () => {
    expect(resolveTimeoutSec(DEFAULT_TASK_TIMEOUT_PRESET, 0)).toBe(0);
    expect(resolveTimeoutSec(DEFAULT_TASK_TIMEOUT_PRESET, 12345)).toBe(0);
  });

  it("resolves a preset to its seconds value", () => {
    expect(resolveTimeoutSec(String(15 * 60), 0)).toBe(900);
    expect(resolveTimeoutSec(String(24 * 60 * 60), 0)).toBe(86400);
  });

  it("resolves a custom numeric value", () => {
    expect(resolveTimeoutSec(CUSTOM_TASK_TIMEOUT_PRESET, 1234)).toBe(1234);
    expect(resolveTimeoutSec(CUSTOM_TASK_TIMEOUT_PRESET, " 600 ")).toBe(600);
  });

  it("returns NaN for a non-numeric custom value", () => {
    expect(Number.isNaN(resolveTimeoutSec(CUSTOM_TASK_TIMEOUT_PRESET, "abc"))).toBe(true);
    expect(Number.isNaN(resolveTimeoutSec(CUSTOM_TASK_TIMEOUT_PRESET, ""))).toBe(true);
  });
});

describe("validateTimeoutSec", () => {
  it("accepts an in-range timeout", () => {
    expect(validateTimeoutSec(60)).toBeNull();
    expect(validateTimeoutSec(MAX_TASK_TIMEOUT_SEC)).toBeNull();
  });

  it("rejects non-numeric, non-integer, and out-of-range values", () => {
    expect(validateTimeoutSec(Number.NaN)).toMatch(/whole number/);
    expect(validateTimeoutSec(1.5)).toMatch(/whole number/);
    expect(validateTimeoutSec(0)).toMatch(/at least/);
    expect(validateTimeoutSec(-5)).toMatch(/at least/);
    expect(validateTimeoutSec(MAX_TASK_TIMEOUT_SEC + 1)).toMatch(/at most/);
  });
});

describe("formatTimeoutSec", () => {
  it("formats common intervals", () => {
    expect(formatTimeoutSec(600)).toBe("10m");
    expect(formatTimeoutSec(3600)).toBe("1h");
    expect(formatTimeoutSec(90)).toBe("90s");
    expect(formatTimeoutSec(0)).toBe("");
  });
});

describe("buildTaskSubmitPayload", () => {
  it("carries a non-zero timeoutSec through to the submit payload", () => {
    const payload = buildTaskSubmitPayload(baseForm({ timeoutSec: 7200 }));
    expect(payload.timeoutSec).toBe(7200);
  });

  it("sends timeoutSec 0 for the default selection", () => {
    const payload = buildTaskSubmitPayload(baseForm({ timeoutSec: 0 }));
    expect(payload.timeoutSec).toBe(0);
  });

  it("omits session fields for one-shot tasks", () => {
    const payload = buildTaskSubmitPayload(
      baseForm({ sessionMode: "", ttlHours: 72, pauseReason: "ignored" }),
    );
    expect(payload.sessionMode).toBe("");
    expect(payload.ttlHours).toBe(0);
    expect(payload.pauseReason).toBe("");
  });

  it("keeps TTL and pause reason for resumable tasks", () => {
    const payload = buildTaskSubmitPayload(
      baseForm({ sessionMode: "resumable", ttlHours: 48, pauseReason: "awaiting review" }),
    );
    expect(payload.sessionMode).toBe("resumable");
    expect(payload.ttlHours).toBe(48);
    expect(payload.pauseReason).toBe("awaiting review");
  });
});
