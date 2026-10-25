package engine

import (
	"bytes"
)

type commentGateWriter struct {
	bytes.Buffer
	entered, release chan struct{}
	first            bool
}

func (w *commentGateWriter) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.Write(p)
}

// Dump uses io.WriteString, so intercept the promoted Buffer fast path too.
func (w *commentGateWriter) WriteString(s string) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.WriteString(s)
}
