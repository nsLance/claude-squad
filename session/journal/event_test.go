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

func TestHandoff_Validate(t *testing.T) {
	cases := []struct {
		name string
		h    Handoff
		err  string
	}{
		{"empty rejected", Handoff{}, "at least one"},
		{"role only", Handoff{Role: "developer"}, ""},
		{"awaiting only", Handoff{Awaiting: "qa"}, ""},
		{"phase only", Handoff{Phase: "verification"}, ""},
		{"next only", Handoff{Next: "ship the codex adapter"}, ""},
		{"full payload", Handoff{Role: "developer", Awaiting: "qa", Phase: "awaiting-qa", Next: "validate"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.h.Validate()
			if c.err == "" {
				if err != nil {
					t.Fatalf("got error %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("got %v, want error containing %q", err, c.err)
			}
		})
	}
}

func TestHandoffEvent_Marshal(t *testing.T) {
	h := Handoff{Role: "developer", Awaiting: "qa", Phase: "awaiting-qa", Next: "validate throttle behavior"}
	ev := HandoffEvent(AgentRef{Name: AgentCS}, "implementation complete; unit tests in place", h)
	if ev.Type != TypeHandoff || ev.Summary == "" || ev.Handoff == nil || ev.Handoff.Role != "developer" {
		t.Fatalf("HandoffEvent shape wrong: %+v", ev)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal handoff event: %v", err)
	}
	for _, want := range []string{
		`"summary":"implementation complete`,
		`"handoff":{`,
		`"role":"developer"`,
		`"awaiting":"qa"`,
		`"phase":"awaiting-qa"`,
		`"next":"validate throttle behavior"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshaled event missing %q: %s", want, b)
		}
	}

	// Other event types must not leak a handoff field.
	pb, _ := json.Marshal(PromptEvent(AgentRef{Name: AgentClaudeCode}, "hi"))
	if strings.Contains(string(pb), `"handoff"`) {
		t.Errorf("prompt event leaked handoff field: %s", pb)
	}
}

func TestDecision_Validate(t *testing.T) {
	cases := []struct {
		name string
		d    Decision
		err  string
	}{
		{"empty rejected", Decision{}, "title"},
		{"title only", Decision{Title: "use sqlite"}, "decision body"},
		{"blank title", Decision{Title: "   ", Decision: "x"}, "title"},
		{"blank body", Decision{Title: "use sqlite", Decision: "  "}, "decision body"},
		{"minimal valid", Decision{Title: "use sqlite", Decision: "ship as embedded store"}, ""},
		{"full payload", Decision{
			Title:        "use sqlite for the worklog index",
			Context:      "tsv is fragile under concurrent appends",
			Decision:     "ship sqlite with WAL",
			Consequences: "binary now links libsqlite, journal compaction needs vacuum",
			Options:      "tsv (current); jsonl; sqlite",
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.d.Validate()
			if c.err == "" {
				if err != nil {
					t.Fatalf("got error %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Fatalf("got %v, want error containing %q", err, c.err)
			}
		})
	}
}

func TestDecisionEvent_Marshal(t *testing.T) {
	d := Decision{Title: "use sqlite", Decision: "ship as embedded store", Context: "tsv fragile"}
	ev := DecisionEvent(AgentRef{Name: AgentHuman}, d)
	if ev.Type != TypeDecision || ev.Decision == nil || ev.Decision.Title != "use sqlite" {
		t.Fatalf("DecisionEvent shape wrong: %+v", ev)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"decision":{`,
		`"title":"use sqlite"`,
		`"decision":"ship as embedded store"`,
		`"context":"tsv fragile"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshaled event missing %q: %s", want, b)
		}
	}
	// Omitted optional fields must not appear in the encoding.
	for _, omit := range []string{`"consequences"`, `"options"`} {
		if strings.Contains(string(b), omit) {
			t.Errorf("optional field unexpectedly present: %s in %s", omit, b)
		}
	}
}

func TestValidDisposition(t *testing.T) {
	for _, d := range []string{DispositionMerged, DispositionAbandoned, DispositionHandedOff, DispositionOther} {
		if !ValidDisposition(d) {
			t.Errorf("%q should be valid", d)
		}
	}
	for _, d := range []string{"", "MERGED", "shipped", "todo"} {
		if ValidDisposition(d) {
			t.Errorf("%q should be invalid", d)
		}
	}
}

func TestFinish_Validate(t *testing.T) {
	ok := Finish{
		Intent:       "land the codex adapter",
		Work:         "wrote adapter + tests; wired into instance_journal",
		FilesChanged: []string{"session/journal/adapter_codex.go", "session/instance_journal.go"},
		Verification: &Verification{Status: VerificationStatusPassed, Evidence: "go test ./..."},
		Disposition:  DispositionMerged,
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("happy path: %v", err)
	}

	// "no files changed" is a legitimate finish — mirror miagent.
	noFiles := ok
	noFiles.FilesChanged = nil
	noFiles.NoFiles = true
	if err := noFiles.Validate(); err != nil {
		t.Fatalf("no_files: %v", err)
	}

	// Every required field must reject its absence with a message that
	// names the field, so the CLI can surface miagent-style errors.
	cases := []struct {
		name  string
		mut   func(f *Finish)
		token string
	}{
		{"missing intent", func(f *Finish) { f.Intent = "" }, "intent"},
		{"blank intent", func(f *Finish) { f.Intent = "   " }, "intent"},
		{"missing work", func(f *Finish) { f.Work = "" }, "work"},
		{"missing files and no_files=false", func(f *Finish) { f.FilesChanged = nil; f.NoFiles = false }, "files_changed"},
		{"missing verification", func(f *Finish) { f.Verification = nil }, "verification"},
		{"bad verification status", func(f *Finish) { f.Verification = &Verification{Status: "ok"} }, "invalid verification status"},
		{"not-run without reason", func(f *Finish) { f.Verification = &Verification{Status: VerificationStatusNotRun} }, "reason"},
		{"missing disposition", func(f *Finish) { f.Disposition = "" }, "disposition"},
		{"bad disposition", func(f *Finish) { f.Disposition = "shipped" }, "disposition"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := ok
			f.Verification = &Verification{Status: VerificationStatusPassed}
			c.mut(&f)
			err := f.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", c.token)
			}
			if !strings.Contains(err.Error(), c.token) {
				t.Errorf("error %q does not contain %q", err.Error(), c.token)
			}
		})
	}
}

func TestFinishEvent_Marshal(t *testing.T) {
	f := Finish{
		Intent:       "x",
		Work:         "y",
		FilesChanged: []string{"a.go", "b.go"},
		Verification: &Verification{Status: VerificationStatusPassed, Evidence: "tests pass"},
		Disposition:  DispositionMerged,
	}
	ev := FinishEvent(AgentRef{Name: AgentHuman}, f)
	if ev.Type != TypeFinish || ev.Finish == nil || ev.Finish.Disposition != DispositionMerged {
		t.Fatalf("FinishEvent shape wrong: %+v", ev)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"finish":{`,
		`"intent":"x"`,
		`"work":"y"`,
		`"files_changed":["a.go","b.go"]`,
		`"verification":{"status":"passed"`,
		`"disposition":"merged"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("marshaled event missing %q: %s", want, b)
		}
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
