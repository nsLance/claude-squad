package journal

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidVerificationStatus(t *testing.T) {
	for _, status := range []string{
		VerificationStatusNotRun,
		VerificationStatusPartial,
		VerificationStatusPassed,
		VerificationStatusFailed,
		VerificationStatusNA,
	} {
		if !ValidVerificationStatus(status) {
			t.Errorf("%q should be valid", status)
		}
	}
	for _, status := range []string{"", "ok", "PASSED", "skipped"} {
		if ValidVerificationStatus(status) {
			t.Errorf("%q should be invalid", status)
		}
	}
}

func TestVerification_Validate(t *testing.T) {
	cases := []struct {
		name string
		v    Verification
		err  string // substring; "" means must succeed
	}{
		{"passed-no-reason", Verification{Status: VerificationStatusPassed}, ""},
		{"passed-with-evidence", Verification{Status: VerificationStatusPassed, Evidence: "go test ./..."}, ""},
		{"not-run-requires-reason", Verification{Status: VerificationStatusNotRun}, "reason"},
		{"not-run-with-blank-reason", Verification{Status: VerificationStatusNotRun, Reason: "   "}, "reason"},
		{"not-run-with-reason", Verification{Status: VerificationStatusNotRun, Reason: "no test infra"}, ""},
		{"unknown-status", Verification{Status: "ok"}, "invalid"},
		{"empty-status", Verification{}, "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.v.Validate()
			if c.err == "" {
				if err != nil {
					t.Fatalf("got error %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil error, want one containing %q", c.err)
			}
			if !strings.Contains(err.Error(), c.err) {
				t.Errorf("error %q does not contain %q", err.Error(), c.err)
			}
		})
	}
}

func TestIntentAndVerificationEvent_Marshal(t *testing.T) {
	intent := IntentEvent(AgentRef{Name: AgentHuman}, "ship the codex adapter")
	if intent.Type != TypeIntent || intent.Text != "ship the codex adapter" {
		t.Fatalf("IntentEvent shape wrong: %+v", intent)
	}

	v := Verification{
		Status:   VerificationStatusPassed,
		Evidence: "go test ./... -count=1 all green",
	}
	ev := VerificationEvent(AgentRef{Name: AgentCS}, v)
	if ev.Type != TypeVerification || ev.Verification == nil || ev.Verification.Status != VerificationStatusPassed {
		t.Fatalf("VerificationEvent shape wrong: %+v", ev)
	}

	// The verification block should serialize as a nested object; absent on
	// events that don't carry one (omitempty).
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal verification event: %v", err)
	}
	if !strings.Contains(string(b), `"verification":{"status":"passed"`) {
		t.Errorf("verification field missing from marshaled event: %s", b)
	}

	// A prompt event must not carry a verification field.
	pb, err := json.Marshal(PromptEvent(AgentRef{Name: AgentClaudeCode}, "hi"))
	if err != nil {
		t.Fatalf("marshal prompt event: %v", err)
	}
	if strings.Contains(string(pb), `"verification"`) {
		t.Errorf("prompt event leaked verification field: %s", pb)
	}
}
