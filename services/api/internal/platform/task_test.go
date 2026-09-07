package platform

import "testing"

func TestTaskRunStateMachine(t *testing.T) {
	valid := [][2]string{{"pending", "inspecting"}, {"inspecting", "awaiting_approval"}, {"awaiting_approval", "approved"}, {"awaiting_approval", "rejected"}, {"pending", "cancelled"}, {"inspecting", "cancelled"}, {"approved", "cancelled"}}
	for _, transition := range valid {
		if !ValidTaskRunTransition(transition[0], transition[1]) {
			t.Fatalf("expected %s -> %s", transition[0], transition[1])
		}
	}
	invalid := [][2]string{{"completed", "inspecting"}, {"rejected", "approved"}, {"approved", "rejected"}, {"pending", "approved"}}
	for _, transition := range invalid {
		if ValidTaskRunTransition(transition[0], transition[1]) {
			t.Fatalf("unexpected %s -> %s", transition[0], transition[1])
		}
	}
	if IsTerminalTaskRunStatus("approved") {
		t.Fatal("approved is an authorization checkpoint, not a terminal state")
	}
	for _, status := range []string{"rejected", "cancelled", "failed", "timed_out", "completed"} {
		if !IsTerminalTaskRunStatus(status) {
			t.Fatalf("expected %s to be terminal", status)
		}
	}
}
