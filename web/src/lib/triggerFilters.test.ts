import { describe, expect, it } from "vitest";
import { filterByOwnership, filterByTriggerType, isGitManaged } from "./triggerFilters";

describe("isGitManaged", () => {
  it("treats a trigger with a source as Git-managed", () => {
    expect(isGitManaged({ sourceId: "defs_default" })).toBe(true);
  });

  it("treats a trigger without a source as a database draft", () => {
    expect(isGitManaged({ sourceId: "" })).toBe(false);
  });
});

describe("filterByOwnership", () => {
  // Fixtures state sourceId explicitly: an object literal with no overlapping
  // property trips TypeScript's weak-type check.
  const draft = { name: "draft", sourceId: "" };
  const managed = { name: "managed", sourceId: "defs_default" };

  it("keeps both kinds when both toggles are on", () => {
    expect(filterByOwnership([draft, managed], true, true)).toEqual([draft, managed]);
  });

  it("keeps only drafts when Git-managed is off", () => {
    expect(filterByOwnership([draft, managed], false, true)).toEqual([draft]);
  });

  it("keeps only Git-managed when drafts are off", () => {
    expect(filterByOwnership([draft, managed], true, false)).toEqual([managed]);
  });

  it("keeps nothing when both toggles are off", () => {
    expect(filterByOwnership([draft, managed], false, false)).toEqual([]);
  });

  it("preserves the original order", () => {
    expect(filterByOwnership([managed, draft], true, true)).toEqual([managed, draft]);
  });
});

describe("filterByTriggerType", () => {
  const cron = { triggerType: "cron" };
  const issue = { triggerType: "issue" };
  const prReview = { triggerType: "pr_review" };
  const all = [cron, issue, prReview];

  it("short-circuits when every type is enabled", () => {
    expect(filterByTriggerType(all, true, true, true)).toEqual(all);
  });

  it("selects a single type", () => {
    expect(filterByTriggerType(all, true, false, false)).toEqual([cron]);
    expect(filterByTriggerType(all, false, true, false)).toEqual([issue]);
    expect(filterByTriggerType(all, false, false, true)).toEqual([prReview]);
  });

  it("keeps unknown types when any filter is active", () => {
    const other = { triggerType: "webhook" };
    expect(filterByTriggerType([other], true, false, false)).toEqual([other]);
  });

  it("keeps nothing when no type is enabled", () => {
    expect(filterByTriggerType(all, false, false, false)).toEqual([]);
  });
});
