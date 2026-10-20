package util

import (
	"crypto/rand"
	"fmt"
	"time"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// RandomID returns a cryptographically random lowercase-alphanumeric string.
// Port of app/util/ids.py::random_id.
func RandomID(length int) string {
	if length <= 0 {
		return ""
	}
	buf := make([]byte, length)
	random := make([]byte, length)
	_, _ = rand.Read(random)
	for i := range buf {
		buf[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(buf)
}

// CompletionID returns `chatcmpl-<24>`.
func CompletionID() string { return "chatcmpl-" + RandomID(24) }

// CallID returns `call_<12>`.
func CallID() string { return "call_" + RandomID(12) }

// UnixNow returns the current Unix time in seconds.
func UnixNow() int64 { return time.Now().Unix() }

// FormatCallID is a tiny helper used where Python used f-strings.
func FormatCallID(prefix, id string) string { return fmt.Sprintf("%s_%s", prefix, id) }
