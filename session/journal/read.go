package journal

import (
	"bufio"
	"encoding/json"
	"os"
)

// LastCheckpoint scans the journal at path and returns the Signature of the
// most recent checkpoint or handoff event, or nil if the chain is empty.
// Unparseable lines are skipped — a corrupt line never hides earlier history.
func LastCheckpoint(path string) (*Signature, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var last *Signature
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if (e.Type == TypeCheckpoint || e.Type == TypeHandoff) && e.Signature != nil {
			last = e.Signature
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return last, nil
}
