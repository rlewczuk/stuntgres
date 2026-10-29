package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 vacuumlazy.c:vacuum_error_callback reports a physical coordinate
// appropriate to the actual work at cancellation. UNKNOWN and a truncation
// phase without a valid block emit no context. Match whole source-defined
// variants, exact fixture relations/indexes, and only variable numeric
// coordinates; every other portable diagnostic field remains exact.
func vacuumProgressCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, pid int64, table string, cancel context.CancelFunc, join func() error) {
	t.Helper()
	if b.PG {
		var signaled bool
		if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatalf("VACUUM cancel=%v err=%v", signaled, err)
		}
	} else {
		cancel()
	}
	err := join()
	if b.PG {
		got := fixture.ExtractDiagnostic(t, err)
		relation := regexp.QuoteMeta("public." + table)
		index := regexp.QuoteMeta(table) + `_(?:id|payload)`
		pattern := regexp.MustCompile(`^(?:while (?:scanning|vacuuming) (?:block [0-9]+(?: offset [1-9][0-9]*)? of )?relation "` + relation + `"|while (?:vacuuming|cleaning up) index "` + index + `" of relation "` + relation + `"|while truncating relation "` + relation + `" to [0-9]+ blocks)?$`)
		if !pattern.MatchString(got.Context) {
			t.Fatalf("unexpected VACUUM cancellation Context for %s: %q", table, got.Context)
		}
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		want.Context = got.Context
		fixture.AssertDiagnostic(t, err, &want)
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("VACUUM cancellation: %v", err)
	}
	if row, present := vacuumProgressScan(t, observer.QueryRowContext(p.Ctx, vacuumProgressQuery, pid)); present {
		t.Fatalf("canceled VACUUM retained progress: %+v", row)
	}
}
