package fixture

import "io"

type ReferenceRangeErrorReader struct{ Err error }

func (r *ReferenceRangeErrorReader) Read([]byte) (int, error) { return 0, r.Err }

type ReferenceRangeGateReader struct {
	io.Reader
	Entered, Release chan struct{}
	started          bool
}

func (r *ReferenceRangeGateReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		close(r.Entered)
		<-r.Release
	}
	return r.Reader.Read(p)
}
