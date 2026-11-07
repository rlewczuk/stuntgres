//go:build !noservertests

package reference_test

import (
	"testing"
)

// serverStep is one Sync-terminated message group and the check for its
// response, which ends at ReadyForQuery.
type serverStep struct {
	packets [][]byte
	check   func([]serverFrame)
}

// serverSteps pipelines the groups: it writes all of them at once, reads every
// response, then checks them in order. The server sees the same message
// sequence as with one serverCycle per group, because no group depends on an
// earlier response. Reading everything before checking keeps the session in
// step after a failed check. Callers keep a pipeline small (one matrix cell or
// one pair's denied set), far below the socket buffers, so the write cannot
// block on unread responses.
func serverSteps(t *testing.T, w *serverWire, steps []serverStep) {
	t.Helper()
	var batch []byte
	for _, s := range steps {
		for _, p := range s.packets {
			batch = append(batch, p...)
		}
		batch = append(batch, serverPacket('S', nil)...)
	}
	w.send(t, batch)
	responses := make([][]serverFrame, len(steps))
	for i := range steps {
		responses[i] = w.ready(t)
	}
	for i, s := range steps {
		s.check(responses[i])
	}
}
func serverColumns(t *testing.T, frames []serverFrame) []serverColumn {
	t.Helper()
	descriptions := serverDescriptions(t, frames)
	if len(descriptions) == 0 {
		t.Fatal("missing RowDescription")
	}
	return descriptions[0]
}
