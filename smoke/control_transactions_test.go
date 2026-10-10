package smoke_test

import "io"

// controlSmokeReader signals entry before blocking in the underlying reader.
// Retained for the unrelated ALTER TYPE control scenarios.
type controlSmokeReader struct {
	io.Reader
	entered chan struct{}
}

func (r *controlSmokeReader) Read(p []byte) (int, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
	}
	return r.Reader.Read(p)
}
