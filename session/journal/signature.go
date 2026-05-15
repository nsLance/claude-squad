package journal

import (
	"crypto/sha256"
	"encoding/hex"
)

// ComputeSignature returns the hex sha256 checkpoint hash over the input:
//
//	prev || 0x00 || rangeBytes || 0x00 || summary || 0x00 || gitSHA
//
// rangeBytes is the raw, immutable journal bytes appended since the previous
// checkpoint. Hashing the raw on-disk bytes — rather than re-marshaled events —
// sidesteps JSON canonicalization entirely: the log is append-only, so the
// bytes never change. prev is the previous checkpoint's Hash, "" for the first
// checkpoint in the chain. The result chains each checkpoint to its
// predecessor, so tampering with any earlier byte invalidates every later hash.
func ComputeSignature(prev string, rangeBytes []byte, summary, gitSHA string) string {
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte{0})
	h.Write(rangeBytes)
	h.Write([]byte{0})
	h.Write([]byte(summary))
	h.Write([]byte{0})
	h.Write([]byte(gitSHA))
	return hex.EncodeToString(h.Sum(nil))
}
