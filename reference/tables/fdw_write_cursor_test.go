package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWriteParticipatingCursor(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	// Explicit provider source SQL ordering, not a portable SQL bag-order claim.
	r.writer.orderedScans = true
	defer func() { r.writer.orderedScans = false }()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_participant_cursor NO SCROLL CURSOR FOR SELECT id,label,flag FROM fdw_data", 0))
	begins, ends := r.writer.statementBegins, r.writer.statementEnds
	for i := range 2 {
		want := [][]any{{int64(1), "one", true}}
		if i == 1 {
			want = [][]any{{int64(2), nil, false}}
		}
		run(fdwRows("FETCH 1 FROM fdw_participant_cursor", want))
		if r.writer.statementBegins-begins != i+1 || r.writer.statementEnds-ends != i+1 {
			t.Fatal("retained scan did not join current statement", r.writer.statementBegins-begins, r.writer.statementEnds-ends)
		}
	}
	run(fixture.Exec("UPDATE fdw_data SET label='cursor-write' WHERE id=2", 1))
	run(fixture.Exec("CLOSE fdw_participant_cursor", 0))
	run(fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=2", [][]any{{int64(2), "cursor-write", false}}))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=2", [][]any{{int64(2), nil, false}}))
	run(fixture.Exec("ROLLBACK", 0))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=2", [][]any{{int64(2), nil, false}}))
}
