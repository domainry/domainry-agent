package product

import (
	"strings"
	"testing"
)

func TestScheduledPlansEnvironmentIsOptionalCompleteAndOriginBound(t *testing.T) {
	keys := []string{"SCHEDULER_SAAS_ENDPOINT", "SCHEDULER_SAAS_TOKEN", "SCHEDULER_CAPABILITY_CONTRACT_SHA256"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	plans, closePlans, err := OpenScheduledPlansFromEnvironment(t.Context(), "runtime")
	if plans != nil || err != nil {
		t.Fatal("unconfigured optional Scheduler service", err)
	}
	closePlans()

	values := []string{"http://127.0.0.1:1", "private-scheduler-token", strings.Repeat("a", 64)}
	for index, key := range keys {
		t.Setenv(key, values[index])
		if index < len(keys)-1 {
			if _, _, err := OpenScheduledPlansFromEnvironment(t.Context(), "runtime"); err == nil || strings.Contains(err.Error(), values[1]) {
				t.Fatal("incomplete Scheduler config accepted or secret exposed", err)
			}
		}
	}

	for _, endpoint := range []string{"http://scheduler.example.test", "https://user:secret@example.test", "https://scheduler.example.test/private", "https://scheduler.example.test?token=secret", "https://scheduler.example.test#secret"} {
		t.Setenv(keys[0], endpoint)
		if _, _, err := OpenScheduledPlansFromEnvironment(t.Context(), "runtime"); err == nil || strings.Contains(err.Error(), values[1]) {
			t.Fatal("unsafe Scheduler endpoint accepted or secret exposed", err)
		}
	}
}
