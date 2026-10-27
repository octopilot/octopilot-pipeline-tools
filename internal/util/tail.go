package util

import "sync"

// TailWriter keeps the last Max bytes written to it, for classifying a tool's failure from its output. Safe for
// concurrent writers (pack's lifecycle output arrives from several goroutines).
type TailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

// NewTailWriter keeps the last max bytes.
func NewTailWriter(max int) *TailWriter { return &TailWriter{max: max} }

func (w *TailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - w.max; over > 0 {
		w.buf = append(w.buf[:0:0], w.buf[over:]...)
	}
	return len(p), nil
}

// String is what is kept.
func (w *TailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// Reset drops what is kept.
func (w *TailWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = w.buf[:0]
}
