import assert from "node:assert/strict";
import test from "node:test";
import { scheduleTriggerLabel, scheduleUpdateBody, type SchedulePlan } from "./schedule-state.ts";

const weekly: SchedulePlan = {
  id: "plan-1", name: "每周一整理待办", kind: "background_task", timezone: "Asia/Shanghai",
  trigger: { type: "recurring", schedule: { type: "weekly_at", time_of_day: "09:00", day_of_week: "monday" } },
  details: { goal: "整理待办", allowed_tools: ["todo_list"] }, status: "enabled", revision: 3,
  created_at: "2026-09-12T00:00:00Z", updated_at: "2026-09-12T00:00:00Z",
};

test("schedule view labels structured weekly plans", () => {
  assert.equal(scheduleTriggerLabel(weekly), "每周一 09:00");
});

test("schedule update keeps kind-specific boundary and exact revision", () => {
  assert.deepEqual(scheduleUpdateBody(weekly, { name: " 每周二整理待办 ", timezone: "Asia/Shanghai", time: "10:30", day: "tuesday", details: weekly.details }), {
    expected_revision: 3, name: "每周二整理待办", timezone: "Asia/Shanghai",
    trigger: { type: "recurring", schedule: { type: "weekly_at", time_of_day: "10:30", day_of_week: "tuesday" } },
    details: { goal: "整理待办", allowed_tools: ["todo_list"] },
  });
});

test("follow-up update preserves its explicit completion condition", () => {
  const followUp: SchedulePlan = { ...weekly, id: "plan-follow", kind: "follow_up", details: { ...weekly.details, completion_condition: "所有阻塞项完成" } };
  const body = scheduleUpdateBody(followUp, { name: followUp.name, timezone: followUp.timezone, time: "09:00", day: "monday", details: followUp.details });
  assert.equal(body.details.completion_condition, "所有阻塞项完成");
  assert.equal(followUp.kind, "follow_up");
});
