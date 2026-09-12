export type ScheduleRule = {
  type: "daily_at" | "weekly_at" | "monthly_at";
  time_of_day: string;
  day_of_week?: string;
  day_of_month?: number;
};

export type ScheduleTrigger =
  | { type: "once"; at: string }
  | { type: "recurring"; schedule: ScheduleRule };

export type SchedulePlan = {
  id: string;
  name: string;
  kind: "background_task" | "follow_up" | "reminder";
  timezone: string;
  trigger: ScheduleTrigger;
  details: { goal?: string; input?: string; allowed_tools?: string[]; completion_condition?: string; title?: string; message?: string };
  status: "enabled" | "disabled" | "paused";
  revision: number;
  conversation_id?: string;
  run_id?: string;
  created_at: string;
  updated_at: string;
};

export type ScheduleOutput = {
  operation: "create" | "list" | "get" | "update" | "pause" | "resume" | "delete";
  request_sha256: string;
  plan?: SchedulePlan;
  items?: SchedulePlan[];
  next_cursor?: string;
  plan_id?: string;
  revision?: number;
  deleted?: boolean;
  replay?: boolean;
};

export const scheduleStatusLabel = (status: SchedulePlan["status"]) =>
  ({ enabled: "运行中", paused: "已暂停", disabled: "已停用" })[status];

const weekdays: Record<string, string> = { monday: "周一", tuesday: "周二", wednesday: "周三", thursday: "周四", friday: "周五", saturday: "周六", sunday: "周日" };

export function scheduleTriggerLabel(plan: SchedulePlan) {
  if (plan.trigger.type === "once") return `${new Date(plan.trigger.at).toLocaleString()}（一次）`;
  const rule = plan.trigger.schedule;
  if (rule.type === "daily_at") return `每天 ${rule.time_of_day}`;
  if (rule.type === "weekly_at") return `每${weekdays[rule.day_of_week || ""] || rule.day_of_week} ${rule.time_of_day}`;
  return `每月 ${rule.day_of_month} 日 ${rule.time_of_day}`;
}

export function scheduleUpdateBody(plan: SchedulePlan, patch: { name: string; timezone: string; time: string; day: string; details: SchedulePlan["details"] }) {
  let trigger: ScheduleTrigger;
  if (plan.trigger.type === "once") {
    trigger = { type: "once", at: patch.time };
  } else {
    const current = plan.trigger.schedule;
    const schedule: ScheduleRule = { type: current.type, time_of_day: patch.time };
    if (current.type === "weekly_at") schedule.day_of_week = patch.day;
    if (current.type === "monthly_at") schedule.day_of_month = Number(patch.day);
    trigger = { type: "recurring", schedule };
  }
  return { expected_revision: plan.revision, name: patch.name.trim(), timezone: patch.timezone.trim(), trigger, details: patch.details };
}
