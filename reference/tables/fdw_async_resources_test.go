package reference_test

import "testing"

// High-water values are acquired with the resource, not inferred from cumulative
// Begin/ReScan/DECLARE counts. Synchronous HTTP reads still own a request, but
// only requests adopted through the optional async callback are async resources.
type fdwAsyncResources struct {
	scans, requests, asynchronous, handlers, cursors              int
	peakScans, peakRequests, peakAsync, peakHandlers, peakCursors int
}

func (r *fdwAsyncReader) resources() fdwAsyncResources {
	r.mu.Lock()
	state := fdwAsyncResources{scans: len(r.scans), requests: len(r.jobs), asynchronous: r.ownedAsyncRequests, handlers: r.active,
		peakScans: r.peakScans, peakRequests: r.peakRequests, peakAsync: r.peakAsyncRequests, peakHandlers: r.peakHandlers}
	r.mu.Unlock()
	r.fdwSQLReader.mu.Lock()
	state.cursors, state.peakCursors = r.openCursors, r.peakOpenCursors
	r.fdwSQLReader.mu.Unlock()
	return state
}

func (c *fdwAsyncCase) invocationResources(t *testing.T, bound int, terminal bool) {
	t.Helper()
	if c.b.PG {
		return
	}
	state := c.reader.resources()
	if state.peakScans > bound || state.peakCursors > bound || state.peakRequests > bound || state.peakHandlers > bound {
		t.Fatalf("invocation exceeded fixed simultaneous resource bound %d: %+v", bound, state)
	}
	if state.peakAsync != 0 || state.asynchronous != 0 {
		t.Fatalf("invocation retained optional async requests instead of the reusable synchronous frontier: %+v", state)
	}
	if state.requests != 0 || state.handlers != 0 || terminal && (state.scans != 0 || state.cursors != 0) {
		t.Fatalf("invocation boundary retained live resources: %+v", state)
	}
}
