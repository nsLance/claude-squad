package config

import "testing"

func TestMergeResumeIntoProgram(t *testing.T) {
	cases := []struct {
		name    string
		program string
		resume  string
		want    string
	}{
		{
			name:    "codex flags fold into resume",
			program: "codex --sandbox workspace-write --ask-for-approval on-request",
			resume:  "codex resume --last",
			want:    "codex --sandbox workspace-write --ask-for-approval on-request resume --last",
		},
		{
			name:    "claude full path keeps path on resume",
			program: "/Users/x/.local/bin/claude",
			resume:  "claude --continue",
			want:    "/Users/x/.local/bin/claude --continue",
		},
		{
			name:    "aider model flag preserved",
			program: "aider --model gpt-4",
			resume:  "aider --restore-chat-history",
			want:    "aider --model gpt-4 --restore-chat-history",
		},
		{
			name:    "different base agent left untouched (custom wrapper)",
			program: "codex --sandbox workspace-write",
			resume:  "my-wrapper --continue",
			want:    "my-wrapper --continue",
		},
		{
			name:    "resume with no extra args returns program",
			program: "gemini --foo",
			resume:  "gemini",
			want:    "gemini --foo",
		},
		{
			name:    "empty resume falls back to program",
			program: "codex --sandbox workspace-write",
			resume:  "",
			want:    "codex --sandbox workspace-write",
		},
		{
			name:    "empty program falls back to resume",
			program: "",
			resume:  "codex resume --last",
			want:    "codex resume --last",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MergeResumeIntoProgram(tc.program, tc.resume); got != tc.want {
				t.Errorf("MergeResumeIntoProgram(%q, %q) = %q, want %q", tc.program, tc.resume, got, tc.want)
			}
		})
	}
}
