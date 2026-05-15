package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Journal is an append-only JSONL writer for one session's event log. It is
// safe for concurrent Append calls. One JSON object per line.
type Journal struct {
	mu   sync.Mutex
	f    *os.File
	path string

	// Envelope defaults stamped onto every appended event.
	sessionID    string
	sessionTitle string
	workspace    string
}

// Open opens (creating if absent, along with its parent dir) the journal at
// path for append. isNew reports whether the file was just created, so the
// caller knows to write the one-per-file header event.
func Open(path string, sess SessionRef, workspace string) (j *Journal, isNew bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, fmt.Errorf("create journal dir: %w", err)
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		isNew = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open journal: %w", err)
	}
	return newJournal(f, path, sess, workspace), isNew, nil
}

// Reopen opens an existing journal for append. Used by the `cs checkpoint`
// subprocess, which must never create a file or write a header — it only
// appends to a journal the parent process already established.
func Reopen(path string, sess SessionRef, workspace string) (*Journal, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("journal not found: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("reopen journal: %w", err)
	}
	return newJournal(f, path, sess, workspace), nil
}

func newJournal(f *os.File, path string, sess SessionRef, workspace string) *Journal {
	return &Journal{
		f:            f,
		path:         path,
		sessionID:    sess.ID,
		sessionTitle: sess.Title,
		workspace:    workspace,
	}
}

// Append stamps the envelope (v, id, ts, session, workspace) onto e and writes
// it as one JSON line. Callers supply only Type, Agent, and payload fields.
func (j *Journal) Append(e Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return errors.New("journal is closed")
	}

	e.V = SchemaVersion
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	e.Session = SessionRef{ID: j.sessionID, Title: j.sessionTitle}
	e.Workspace = j.workspace

	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal journal event: %w", err)
	}
	b = append(b, '\n')
	if _, err := j.f.Write(b); err != nil {
		return fmt.Errorf("write journal event: %w", err)
	}
	return nil
}

// Size returns the current journal file size in bytes. Because every write is
// O_APPEND, this doubles as the offset of the next event — used as the To
// boundary when computing a checkpoint signature.
func (j *Journal) Size() (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return 0, errors.New("journal is closed")
	}
	st, err := j.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// ReadRange returns the raw journal bytes in [from, to). Used to feed the
// immutable, already-written log tail into ComputeSignature.
func (j *Journal) ReadRange(from, to int64) ([]byte, error) {
	if from < 0 || to < from {
		return nil, fmt.Errorf("invalid journal range [%d,%d)", from, to)
	}
	if to == from {
		return nil, nil
	}
	rf, err := os.Open(j.path)
	if err != nil {
		return nil, err
	}
	defer rf.Close()
	buf := make([]byte, to-from)
	if _, err := rf.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf, nil
}

// Path returns the journal file path.
func (j *Journal) Path() string {
	return j.path
}

// Close closes the underlying file. Safe to call more than once.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}
