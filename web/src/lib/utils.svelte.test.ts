// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { renderMarkdown, resumeTaskRoute } from "./utils.svelte";

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
