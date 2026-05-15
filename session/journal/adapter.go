package journal

import "context"

// Adapter passively observes one agent's own transcript output and reports the
// real user prompts it finds. Adapters are best-effort, like the journal: they
// never block or fail a session.
type Adapter interface {
	// Run watches for prompts until ctx is cancelled. It is tail-only: it
	// seeds to the end of any transcript that already exists when Run starts,
	// and never replays earlier history. Each discovered prompt is passed to
	// emit along with the originating agent.
	Run(ctx context.Context, emit func(AgentRef, string))
}
