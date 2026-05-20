package session

import (
	"strings"
	"testing"

	"claude-squad/session/journal"
)

func TestRenderFinishTemplate_Defaults(t *testing.T) {
	got := RenderFinishTemplate("my session", nil)
	for _, want := range []string{
		"# Finish: my session",
		"## Intent",
		"## Work Performed",
		"## Files Changed",
		"## Verification",
		"Status: passed",
		"## Final Disposition",
		"merged",
		"- <path/that/changed.go>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q", want)
		}
	}
}

func TestRenderFinishTemplate_PrefillsFiles(t *testing.T) {
	got := RenderFinishTemplate("s", []string{"a.go", "b/c.go"})
	if !strings.Contains(got, "- a.go") || !strings.Contains(got, "- b/c.go") {
		t.Errorf("prefilled files missing from template:\n%s", got)
	}
	if strings.Contains(got, "<path/that/changed.go>") {
		t.Errorf("placeholder bullet should be replaced when files are prefilled")
	}
}

func TestParseFinishTemplate_HappyPath(t *testing.T) {
	md := `# Finish: my session

## Intent
land the codex adapter

## Work Performed
wrote adapter, wired into instance_journal, tests pass

## Files Changed
- session/journal/adapter_codex.go
- session/instance_journal.go

## Verification
Status: passed
go test ./... -count=1: all green

## Final Disposition
merged
`
	f := ParseFinishTemplate(md)
	if f.Intent != "land the codex adapter" {
		t.Errorf("Intent = %q", f.Intent)
	}
	if !strings.Contains(f.Work, "wrote adapter") {
		t.Errorf("Work = %q", f.Work)
	}
	if len(f.FilesChanged) != 2 ||
		f.FilesChanged[0] != "session/journal/adapter_codex.go" ||
		f.FilesChanged[1] != "session/instance_journal.go" {
		t.Errorf("FilesChanged = %v", f.FilesChanged)
	}
	if f.NoFiles {
		t.Errorf("NoFiles should be false when paths are present")
	}
	if f.Verification == nil || f.Verification.Status != journal.VerificationStatusPassed {
		t.Errorf("Verification = %+v", f.Verification)
	}
	if !strings.Contains(f.Verification.Evidence, "go test ./...") {
		t.Errorf("Evidence = %q", f.Verification.Evidence)
	}
	if f.Disposition != journal.DispositionMerged {
		t.Errorf("Disposition = %q", f.Disposition)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("happy path should validate: %v", err)
	}
}

func TestParseFinishTemplate_NoFilesLiteral(t *testing.T) {
	md := `## Intent
doc tweak only

## Work Performed
edited README

## Files Changed
no files changed

## Verification
Status: n/a

## Final Disposition
merged
`
	f := ParseFinishTemplate(md)
	if !f.NoFiles {
		t.Errorf("expected NoFiles=true for literal 'no files changed'")
	}
	if len(f.FilesChanged) != 0 {
		t.Errorf("expected empty FilesChanged, got %v", f.FilesChanged)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("no-files variant should validate: %v", err)
	}
}

func TestParseFinishTemplate_PlaceholdersTreatedEmpty(t *testing.T) {
	// Untouched template: every section still holds its angle-bracket
	// placeholder. Parsing should produce a Finish whose Validate fails on
	// the FIRST missing field (Intent).
	md := RenderFinishTemplate("s", nil)
	f := ParseFinishTemplate(md)
	if err := f.Validate(); err == nil {
		t.Fatal("untouched template should fail validation")
	} else if !strings.Contains(err.Error(), "intent") {
		t.Errorf("first error should be about intent, got %v", err)
	}
}

func TestParseFinishTemplate_NotRunRequiresReason(t *testing.T) {
	md := `## Intent
x

## Work Performed
y

## Files Changed
no files changed

## Verification
Status: not-run
Reason: no test infra in this repo yet

extra evidence on a later line

## Final Disposition
merged
`
	f := ParseFinishTemplate(md)
	if f.Verification == nil || f.Verification.Status != journal.VerificationStatusNotRun {
		t.Fatalf("Status = %+v", f.Verification)
	}
	if f.Verification.Reason != "no test infra in this repo yet" {
		t.Errorf("Reason = %q", f.Verification.Reason)
	}
	if !strings.Contains(f.Verification.Evidence, "extra evidence") {
		t.Errorf("Evidence = %q", f.Verification.Evidence)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("not-run with reason should validate: %v", err)
	}
}

func TestParseFinishTemplate_StripsHTMLComments(t *testing.T) {
	md := `## Intent
real intent text
<!--
  this comment should not become evidence
-->

## Work Performed
work
## Files Changed
no files changed
## Verification
Status: passed
## Final Disposition
merged
`
	f := ParseFinishTemplate(md)
	if strings.Contains(f.Intent, "this comment") {
		t.Errorf("HTML comment leaked into Intent: %q", f.Intent)
	}
	if f.Intent != "real intent text" {
		t.Errorf("Intent = %q", f.Intent)
	}
}

func TestParseFinishTemplate_DispositionTokenOnly(t *testing.T) {
	md := `## Intent
x
## Work Performed
y
## Files Changed
no files changed
## Verification
Status: passed
## Final Disposition
handed-off — qa picked up
`
	f := ParseFinishTemplate(md)
	if f.Disposition != journal.DispositionHandedOff {
		t.Errorf("Disposition = %q, want %q", f.Disposition, journal.DispositionHandedOff)
	}
}
