package session

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"claude-squad/session/journal"
)

// finishTemplate is the markdown skeleton handed to $EDITOR when the user
// runs `cs finish --interactive`. The placeholder lines in angle brackets
// are recognized by parseFinish and treated as empty, so a section the user
// leaves untouched fails validation with the same error a flag-driven
// invocation would produce.
const finishTemplate = `# Finish: %s

<!--
  Edit the sections below and save+quit to record this session's closeout.
  Quit without saving to abort.

  Required: Intent, Work Performed, Files Changed (or the literal phrase
  "no files changed"), Verification (with a Status line), Final Disposition.

  Allowed Verification statuses: not-run | partial | passed | failed | n/a
  ("not-run" requires a Reason line directly after the Status line.)

  Allowed Final Disposition values: merged | abandoned | handed-off | other
-->

## Intent
<what this session was trying to accomplish — one paragraph>

## Work Performed
<commands run, edits made, key outputs>

## Files Changed
%s

## Verification
Status: passed
<freeform evidence — command output, test results, manual checks>

## Final Disposition
merged
`

// RenderFinishTemplate builds the editor template for a session titled
// title. If filesChanged is non-empty, those paths are pre-filled into the
// Files Changed section; otherwise a single placeholder bullet is used.
func RenderFinishTemplate(title string, filesChanged []string) string {
	if title == "" {
		title = "session"
	}
	var files string
	if len(filesChanged) == 0 {
		files = "- <path/that/changed.go>"
	} else {
		lines := make([]string, 0, len(filesChanged))
		for _, p := range filesChanged {
			lines = append(lines, "- "+p)
		}
		files = strings.Join(lines, "\n")
	}
	return fmt.Sprintf(finishTemplate, title, files)
}

// ParseFinishTemplate parses the markdown an operator saved in $EDITOR back
// into a Finish struct. Sections still containing their angle-bracket
// placeholder text are treated as empty so validation surfaces them.
// Returns the assembled struct without running Validate — the caller does
// that so it can decide how to handle the error (e.g. preserve the file).
func ParseFinishTemplate(md string) journal.Finish {
	sections := parseSections(md)

	f := journal.Finish{
		Intent: sectionBody(sections, "Intent"),
		Work:   sectionBody(sections, "Work Performed"),
	}

	filesBody := sectionBody(sections, "Files Changed")
	switch {
	case strings.EqualFold(strings.TrimSpace(filesBody), "no files changed"):
		f.NoFiles = true
	default:
		f.FilesChanged = parseBulletList(filesBody)
	}

	f.Verification = parseVerificationSection(sectionBody(sections, "Verification"))

	if d := strings.TrimSpace(sectionBody(sections, "Final Disposition")); d != "" {
		// Disposition can be just the word ("merged") or include trailing
		// commentary on the same line. Take the first whitespace-delimited
		// token so "merged — landed in main" still parses.
		if i := strings.IndexAny(d, " \t\n"); i > 0 {
			d = d[:i]
		}
		f.Disposition = d
	}

	return f
}

// parseSections walks markdown and returns the body text of every
// `## Heading` section in order. Comment blocks (<!-- ... -->) are stripped
// before parsing, the top-of-file `# Title` line is ignored.
func parseSections(md string) map[string]string {
	md = stripHTMLComments(md)
	out := map[string]string{}

	var (
		curName string
		curBody strings.Builder
	)
	flush := func() {
		if curName != "" {
			out[curName] = strings.TrimSpace(curBody.String())
		}
		curName = ""
		curBody.Reset()
	}

	sc := bufio.NewScanner(strings.NewReader(md))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			curName = strings.TrimSpace(strings.TrimPrefix(line, "## "))
		case strings.HasPrefix(line, "# "):
			// Top-level title; ignore even mid-document.
		default:
			if curName != "" {
				curBody.WriteString(line)
				curBody.WriteByte('\n')
			}
		}
	}
	flush()
	return out
}

// sectionBody returns the trimmed body of a section by name. If the body is
// just a placeholder of the form `<...>` it is reported as empty so callers
// see "missing" rather than literal placeholder text.
func sectionBody(sections map[string]string, name string) string {
	body := strings.TrimSpace(sections[name])
	if body == "" {
		return ""
	}
	if isPlaceholder(body) {
		return ""
	}
	return body
}

// isPlaceholder reports whether body is a one-line angle-bracketed hint left
// over from the template, e.g. "<commands run, edits made, key outputs>".
// Multi-line bodies that happen to start with `<` (legitimate XML-tag prose,
// for instance) are not treated as placeholders.
func isPlaceholder(body string) bool {
	if !strings.HasPrefix(body, "<") || !strings.HasSuffix(body, ">") {
		return false
	}
	return !strings.ContainsRune(body, '\n')
}

// parseBulletList returns the trimmed contents of every "- " line in body,
// filtering placeholders. An empty or all-placeholder list returns nil.
func parseBulletList(body string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "- ") && line != "-" {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if item == "" || isPlaceholder(item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// parseVerificationSection extracts the verification block from the section
// body. The first non-blank line must be `Status: <value>`. A `Reason:` line
// may follow on the next non-blank line (required when Status is not-run).
// Everything else is folded into Evidence.
func parseVerificationSection(body string) *journal.Verification {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}

	v := &journal.Verification{}
	var evidence []string
	statusSeen, reasonSeen := false, false

	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case !statusSeen && trimmed == "":
			// Skip leading blanks before Status.
			continue
		case !statusSeen && strings.HasPrefix(trimmed, "Status:"):
			v.Status = strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:"))
			statusSeen = true
		case statusSeen && !reasonSeen && strings.HasPrefix(trimmed, "Reason:"):
			v.Reason = strings.TrimSpace(strings.TrimPrefix(trimmed, "Reason:"))
			reasonSeen = true
		default:
			if isPlaceholder(trimmed) {
				continue
			}
			evidence = append(evidence, line)
		}
	}
	v.Evidence = strings.TrimSpace(strings.Join(evidence, "\n"))
	return v
}

// stripHTMLComments removes every <!-- ... --> block (including newlines)
// before section parsing so editor commentary in the template doesn't end up
// as part of any section body.
func stripHTMLComments(s string) string {
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "-->")
		if j < 0 {
			return s[:i] // unterminated comment — drop the rest defensively
		}
		s = s[:i] + s[i+j+3:]
	}
}

// RunFinishInteractive opens $EDITOR with the finish template, parses the
// saved result, and returns the assembled Finish struct or an error. The
// caller (typically the CLI subcommand) decides what to do with the result.
// title is used in the template header; filesChanged seeds the Files
// Changed section.
//
// On editor abort or a parse-then-validate failure, the temp file is kept
// and its path is returned in the error so the operator can re-run with the
// path or resume editing.
func RunFinishInteractive(title string, filesChanged []string) (journal.Finish, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	tmpDir := os.TempDir()
	stamp := time.Now().UTC().Format("20060102T150405Z")
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("cs-finish-%s.md", stamp))

	if err := os.WriteFile(tmpPath, []byte(RenderFinishTemplate(title, filesChanged)), 0o600); err != nil {
		return journal.Finish{}, fmt.Errorf("write template: %w", err)
	}

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return journal.Finish{}, fmt.Errorf("editor %q exited with error: %w (template kept at %s)", editor, err, tmpPath)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return journal.Finish{}, fmt.Errorf("re-read template: %w (path %s)", err, tmpPath)
	}

	f := ParseFinishTemplate(string(data))
	if err := f.Validate(); err != nil {
		return f, fmt.Errorf("%w (template kept at %s for retry)", err, tmpPath)
	}

	// Clean up only on success — failed attempts keep the file so the user
	// can fix it and re-run with `cs finish --from-template <path>` once we
	// add that flag, or just open it again in their editor.
	_ = os.Remove(tmpPath)
	return f, nil
}
