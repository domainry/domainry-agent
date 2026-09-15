package toolsdk

import "encoding/json"

const (
	ScheduleCreateToolKey = "schedule_create"
	ScheduleListToolKey   = "schedule_list"
	ScheduleGetToolKey    = "schedule_get"
	ScheduleUpdateToolKey = "schedule_update"
	SchedulePauseToolKey  = "schedule_pause"
	ScheduleResumeToolKey = "schedule_resume"
	ScheduleDeleteToolKey = "schedule_delete"
)

// ScheduleDefinitions is the single model-facing protocol for user-owned
// plans. Tools maps these bounded values to Scheduler SDK commands; callers
// cannot provide owner identity, target owners, Action keys, provider routes,
// credentials, or raw cron expressions.
func ScheduleDefinitions() []Definition {
	trigger := json.RawMessage(`{
  "oneOf": [
    {"type":"object","additionalProperties":false,"properties":{"type":{"const":"once"},"at":{"type":"string","format":"date-time"}},"required":["type","at"]},
    {"type":"object","additionalProperties":false,"properties":{"type":{"const":"recurring"},"schedule":{"type":"object","additionalProperties":false,"properties":{"type":{"type":"string","enum":["daily_at","weekly_at","monthly_at"]},"time_of_day":{"type":"string","pattern":"^(?:[01][0-9]|2[0-3]):[0-5][0-9](?::[0-5][0-9])?$"},"day_of_week":{"type":"string","enum":["monday","tuesday","wednesday","thursday","friday","saturday","sunday"]},"day_of_month":{"type":"integer","minimum":1,"maximum":31}},"required":["type","time_of_day"]}},"required":["type","schedule"]}
  ]
}`)
	details := json.RawMessage(`{
  "type":"object","additionalProperties":false,
  "properties":{"goal":{"type":"string","minLength":1,"maxLength":2048},"input":{"type":"string","maxLength":8192},"allowed_tools":{"type":"array","maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":128}},"completion_condition":{"type":"string","minLength":1,"maxLength":2048},"title":{"type":"string","minLength":1,"maxLength":240},"message":{"type":"string","minLength":1,"maxLength":4000}}
}`)
	output := json.RawMessage(`{
  "type":"object","additionalProperties":false,
  "properties":{
    "operation":{"type":"string","enum":["create","list","get","update","pause","resume","delete"]},"request_sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"},
    "plan":{"$ref":"#/$defs/plan"},"items":{"type":"array","items":{"$ref":"#/$defs/plan"}},"next_cursor":{"type":"string"},
    "plan_id":{"type":"string"},"revision":{"type":"integer","minimum":1},"deleted":{"type":"boolean"},"replay":{"type":"boolean"}
  },
  "required":["operation","request_sha256"],
  "$defs":{
    "trigger":{"oneOf":[
      {"type":"object","additionalProperties":false,"properties":{"type":{"const":"once"},"at":{"type":"string","format":"date-time"}},"required":["type","at"]},
      {"type":"object","additionalProperties":false,"properties":{"type":{"const":"recurring"},"schedule":{"type":"object","additionalProperties":false,"properties":{"type":{"type":"string","enum":["daily_at","weekly_at","monthly_at"]},"time_of_day":{"type":"string"},"day_of_week":{"type":"string"},"day_of_month":{"type":"integer"}},"required":["type","time_of_day"]}},"required":["type","schedule"]}
    ]},
    "details":{"type":"object","additionalProperties":false,"properties":{"goal":{"type":"string"},"input":{"type":"string"},"allowed_tools":{"type":"array","items":{"type":"string"}},"completion_condition":{"type":"string"},"title":{"type":"string"},"message":{"type":"string"}}},
    "plan":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string"},"name":{"type":"string"},"kind":{"type":"string","enum":["background_task","follow_up","reminder"]},"timezone":{"type":"string"},"trigger":{"$ref":"#/$defs/trigger"},"details":{"$ref":"#/$defs/details"},"status":{"type":"string","enum":["enabled","disabled","paused"]},"revision":{"type":"integer","minimum":1},"conversation_id":{"type":"string"},"run_id":{"type":"string"},"created_at":{"type":"string","format":"date-time"},"updated_at":{"type":"string","format":"date-time"}},"required":["id","name","kind","timezone","trigger","details","status","revision","created_at","updated_at"]}
  }
}`)
	base := func(key, description, effect, idempotency string, input json.RawMessage) Definition {
		parallelism := ""
		if effect == "read" {
			parallelism = ToolParallelismIndependentRead
		}
		return Definition{Key: key, Version: "2", ActionKey: "agent.conversation_tools." + key, Description: description, InputSchema: input, OutputSchema: output, Effect: effect, Idempotency: idempotency, Parallelism: parallelism, TimeoutMillis: 10000, MaxOutputBytes: 65536}
	}
	createInput := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["background_task","follow_up","reminder"]},"name":{"type":"string","minLength":1,"maxLength":500},"timezone":{"type":"string","minLength":1,"maxLength":128},"trigger":` + string(trigger) + `,"details":` + string(details) + `},"required":["kind","name","trigger","details"]}`)
	listInput := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"status":{"type":"string","enum":["enabled","disabled","paused"]},"cursor":{"type":"string","maxLength":191},"limit":{"type":"integer","minimum":1,"maximum":100}}}`)
	getInput := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","minLength":1,"maxLength":191}},"required":["plan_id"]}`)
	updateInput := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","minLength":1,"maxLength":191},"expected_revision":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":500},"timezone":{"type":"string","minLength":1,"maxLength":128},"trigger":` + string(trigger) + `,"details":` + string(details) + `},"required":["plan_id","expected_revision"],"minProperties":3}`)
	statusInput := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","minLength":1,"maxLength":191},"expected_revision":{"type":"integer","minimum":1}},"required":["plan_id","expected_revision"]}`)
	return []Definition{
		base(ScheduleCreateToolKey, "Create a one-time or recurring user plan. Use kind=background_task for ordinary scheduled work. Use kind=follow_up only for a scoped recurring observation with an explicit completion_condition; the first observation establishes a silent baseline and later notifications are limited to change, completion, failure, or required user action. Use kind=reminder for a bounded title/message notification. Choose only currently available non-task tools. Omit timezone to use the current user's timezone. Use structured daily_at, weekly_at, or monthly_at schedules; never invent cron, owner identity, Action keys, recipients, connections, or provider payloads.", "write", "key", createInput),
		base(ScheduleListToolKey, "List the current user's plans. Follow next_cursor with the same status filter. This returns only background-task and reminder plans managed by this product.", "read", "natural", listInput),
		base(ScheduleGetToolKey, "Read one current user plan and its revision before changing its schedule, content, or status.", "read", "natural", getInput),
		base(ScheduleUpdateToolKey, "Update the name, timezone, trigger, or kind-specific details of one plan using its exact current expected_revision. The plan kind and execution target cannot change.", "write", "reconcile", updateInput),
		base(SchedulePauseToolKey, "Pause an enabled plan using its exact current expected_revision. A paused plan does not trigger until resumed.", "write", "reconcile", statusInput),
		base(ScheduleResumeToolKey, "Resume a paused or disabled plan using its exact current expected_revision. Recurrence continues from the resume time.", "write", "reconcile", statusInput),
		base(ScheduleDeleteToolKey, "Delete one plan using its exact current expected_revision. The Scheduler keeps an internal disabled tombstone so a restart cannot revive it.", "write", "reconcile", statusInput),
	}
}
