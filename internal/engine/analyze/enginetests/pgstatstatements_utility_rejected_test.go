package enginetests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestPgStatStatementsFingerprintUtilityRejectedSyntax(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	execute := func(source string) error {
		op.OriginalSQL, op.SQLAvailable = source, true
		result, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, source), nil)
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); err == nil {
				err = closeErr
			}
		}
		return err
	}
	// The logical module and its settings are active at cluster startup.
	// Enable the real collection policy, not an unrelated query-id setting.
	for _, source := range []string{"SET pg_stat_statements.track='all'", "CREATE TABLE pgss_jumble_reject_base(a integer)"} {
		if err := execute(source); err != nil {
			t.Fatal(source, err)
		}
	}
	// A real successfully collected utility proves that absence below is not a
	// disabled collector, missing SQL provenance, or a synthetic execution seam.
	admitted := false
	for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
		if entry.Query == "CREATE TABLE pgss_jumble_reject_base(a integer)" {
			admitted = true
		}
	}
	if !admitted {
		t.Fatal("successful utility collection witness missing")
	}
	for _, tc := range []struct{ source, marker, code, message string }{
		{"CREATE INDEX pgss_jumble_rejected_index ON pgss_jumble_reject_base(((SELECT 1)))", "pgss_jumble_rejected_index", "0A000", "cannot use subquery in index expression"},
		{"CREATE TABLE pgss_jumble_rejected_generation(a integer,b integer GENERATED ALWAYS AS(sum(a)) STORED)", "pgss_jumble_rejected_generation", "42803", "aggregate functions are not allowed in column generation expressions"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			// The genuine parser's deferred-rejection leaves must not panic in the raw
			// inventory and must still reach the established execution/error authority.
			analyze.JumbleUtility(enginetest.Statement(t, tc.source))
			var diagnostic *sqlstate.Error
			if err := execute(tc.source); !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatalf("real rejected utility diagnostic: %v", err)
			}
			for _, entry := range e.Cluster.StatementStatistics.Snapshot() {
				if strings.Contains(entry.Query, tc.marker) {
					t.Fatalf("rejected utility published a successful entry: %+v", entry)
				}
			}
		})
	}
}
