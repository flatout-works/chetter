// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { RunnerInfoSchema } from "$gen/proto/api/v1/api_pb";
import { renderMarkdown, resumeTaskRoute, runnerHasTelemetry } from "./utils.svelte";

describe("renderMarkdown", () => {
  it("removes executable HTML while preserving safe Markdown", () => {
    const rendered = renderMarkdown(`
**safe text**

<script>alert("script")</script>
<img src="x" onerror="alert('event')">
[unsafe link](javascript:alert('url'))
<svg><script>alert("svg")</script></svg>
`);

    expect(rendered).toContain("<strong>safe text</strong>");
    expect(rendered).not.toMatch(/<script/i);
    expect(rendered).not.toMatch(/onerror/i);
    expect(rendered).not.toMatch(/href=["']javascript:/i);
    expect(rendered).not.toMatch(/<svg/i);
  });

  it("optionally renders line breaks for session exports", () => {
    expect(renderMarkdown("first\nsecond", true)).toContain("first<br>second");
  });
});

describe("runnerHasTelemetry", () => {
  it("is false for null or undefined runners", () => {
    expect(runnerHasTelemetry(null)).toBe(false);
    expect(runnerHasTelemetry(undefined)).toBe(false);
  });

  it("is false for a runner with no telemetry fields", () => {
    expect(runnerHasTelemetry(create(RunnerInfoSchema, { runnerId: "r1" }))).toBe(false);
  });

  it("is true when resource gauges are reported", () => {
    const runner = create(RunnerInfoSchema, {
      runnerId: "r1",
      resource: { cpuPercent: 12.5, memoryPercent: 40, diskPercent: 0 },
    });
    expect(runnerHasTelemetry(runner)).toBe(true);
  });

  it("is true when sandbox availability is reported", () => {
    expect(runnerHasTelemetry(create(RunnerInfoSchema, { sandboxAvailable: true }))).toBe(true);
  });

  it("is true when sandbox metrics are reported", () => {
    const withCounters = create(RunnerInfoSchema, { sandboxTotal: 3n, sandboxStartFailures: 1n });
    expect(runnerHasTelemetry(withCounters)).toBe(true);

    const withTimings = create(RunnerInfoSchema, { sandboxLifetimeMs: 120_000n, sandboxMaxRssMb: 512n });
    expect(runnerHasTelemetry(withTimings)).toBe(true);

    const withPeakCpu = create(RunnerInfoSchema, { sandboxMaxCpuPercent: 75.5 });
    expect(runnerHasTelemetry(withPeakCpu)).toBe(true);
  });

  it("is true when container caps are set", () => {
    const withMemory = create(RunnerInfoSchema, { containerMemoryMb: 2048 });
    expect(runnerHasTelemetry(withMemory)).toBe(true);

    const withCpu = create(RunnerInfoSchema, { containerCpu: 2.5 });
    expect(runnerHasTelemetry(withCpu)).toBe(true);
  });
});

describe("resumeTaskRoute", () => {
  it("returns the task detail route for a newly created resume attempt", () => {
    expect(resumeTaskRoute("task_abc123")).toBe("/tasks/task_abc123");
  });

  it("returns null when the resume did not produce a task", () => {
    expect(resumeTaskRoute(undefined)).toBeNull();
    expect(resumeTaskRoute(null)).toBeNull();
    expect(resumeTaskRoute("")).toBeNull();
  });
});
