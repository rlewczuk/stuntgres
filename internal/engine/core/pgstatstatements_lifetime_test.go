package core

import (
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

// The composed CompletionTerminal and CompletionPortal tests exercise actual
// resources. This private owner test additionally retains the exact scalar token
// so a post-disposal collector record can distinguish release from a frame-only
// finished flag, without any production inspection API.
func TestPgStatStatementsCompletionFrameRelease(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		executed, failed, success bool
		wantCalls, wantRows       int64
	}{
		{"successful", true, false, true, 1, 2},
		{"execution_error", true, true, true, 0, 0},
		{"terminal_rejection", true, false, false, 0, 0},
		{"unused_planning_only", false, false, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(100, now)
			key := statistics.StatementKey{UserID: 10, DatabaseID: 20, QueryID: 30, TopLevel: true}
			token := store.Begin(key, key, "SELECT $1", now)
			frame := statementStatsFrame{store: store, token: token, key: key, terminalKey: key, source: "SELECT 7", utility: true, terminalCaptured: true, terminalEnabled: true, admitted: true, executed: tc.executed, failed: tc.failed, rows: 2, elapsed: time.Millisecond}
			frame.finish(tc.success)
			frame.finish(true)
			// A leaked active admission would accept this real leaf record even
			// though the runtime frame's own finished guard rejected a second Finish.
			store.RecordExecution(token, statistics.StatementRecord{Key: key, Query: "SELECT 99", At: now.Add(time.Second), Milliseconds: 9, Rows: 99})
			var calls, rows int64
			for _, entry := range store.Snapshot() {
				calls += entry.Execution.Count
				rows += entry.Rows
			}
			if calls != tc.wantCalls || rows != tc.wantRows || !frame.finished || frame.source != "" || frame.session != nil {
				t.Fatal("runtime terminal retained a live admission or source", calls, rows, frame.finished, frame.source)
			}
		})
	}
}
