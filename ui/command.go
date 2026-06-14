package ui

import "strings"

// commandAliases maps shorthand/k9s-style verbs to canonical command-bar verbs.
// Canonical verbs: workspaces, sessions, ws, new, quit, help. Entries are only
// needed for non-identity mappings.
var commandAliases = map[string]string{
	"ns":        "workspaces",
	"workspace": "ws",
	"s":         "sessions",
	"sess":      "sessions",
	"all":       "sessions",
	"n":         "new",
	"q":         "quit",
	"exit":      "quit",
	"h":         "help",
	"?":         "help",
}

// ParseCommand splits a command-bar input into a canonical verb and its args.
// Aliases are resolved; unknown verbs pass through unchanged so the caller can
// report them. ok is false for empty/whitespace input.
func ParseCommand(input string) (verb string, args []string, ok bool) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "", nil, false
	}
	verb = strings.ToLower(fields[0])
	if canon, found := commandAliases[verb]; found {
		verb = canon
	}
	return verb, fields[1:], true
}
