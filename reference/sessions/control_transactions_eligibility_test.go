package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlTransactionsFailedRestoreEligibility(t *testing.T) {
	for _, failure := range []string{"syntax", "missing", "control", "reader", "precancel", "readcancel", "aborted"} {
		t.Run(failure, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.CatalogControlSQLWitness(t, p)
			db, a, b := fixture.CatalogControlConnections(t, p, true)
			ctx := context.Background()
			fixture.CatalogControlAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			fixture.CatalogControlAffected(t, ctx, a, "BEGIN", false, 0)
			fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			fixture.CatalogControlAffected(t, ctx, a, "SET application_name TO 'candidate'", false, 0)
			rows, err := b.QueryContext(ctx, "SELECT v FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			switch failure {
			case "reader":
				want := errors.New("read failed")
				err = stuntgres.Restore(ctx, db, fixture.CatalogControlFailingReader{want})
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
			case "precancel", "readcancel":
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				if failure == "precancel" {
					cancel()
					err = stuntgres.Restore(c, db, strings.NewReader("CREATE TABLE x(v INT)"))
				} else {
					err = stuntgres.Restore(c, db, &fixture.CatalogControlCancelReader{Cancel: cancel})
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			default:
				if failure == "aborted" {
					fixture.AssertControlDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", false), fixture.TransactionNull())
				}
				scripts := map[string]string{"syntax": "CREATE TABLE x(v INT); INSERT INTO x VALUES(;", "missing": "CREATE TABLE x(v INT); INSERT INTO missing VALUES(9);", "control": "BEGIN;", "aborted": "BEGIN;"}
				err = stuntgres.Restore(ctx, db, strings.NewReader(scripts[failure]))
				fixture.State(t, err, map[string]string{"syntax": "42601", "missing": "42P01", "control": "0A000", "aborted": "0A000"}[failure])
			}
			fixture.CatalogControlNativeValues(t, rows, 77)
			fixture.CatalogControlSelect(t, ctx, b, "SELECT v FROM t", false, 77)
			if failure == "aborted" {
				_, err = a.ExecContext(ctx, "SELECT v FROM t")
				fixture.AssertControlDiagnostic(t, err, fixture.TransactionAborted())
				fixture.CatalogControlAffected(t, ctx, a, "ROLLBACK", false, 0)
			} else {
				fixture.CatalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
				fixture.CatalogControlAffected(t, ctx, a, "COMMIT", false, 0)
				fixture.CatalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
				var name string
				if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&name); err != nil || name != "candidate" {
					t.Fatal(name, err)
				}
			}
		})
	}
}
