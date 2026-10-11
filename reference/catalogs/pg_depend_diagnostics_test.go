package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, tc := range []struct{ q, msg, pos string }{
			{"SELECT oid FROM pg_catalog.pg_depend", "column \"oid\" does not exist", "8"},
			{"SELECT missing FROM pg_catalog.pg_depend", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_depend", "column \"missing\" does not exist", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.msg, Position: tc.pos})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_depend WHERE objid=$1", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		q := "SELECT * FROM pg_catalog.pg_depend WHERE FALSE"
		rows, err := b.DB.QueryContext(canceled, q)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err := b.DB.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled statement")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err = b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(canceled)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.PgDependSelect(q, [][]any{}))
		if !b.PG { // Never mutate physical PG superuser catalogs for parity.
			for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_catalog.pg_depend VALUES(1259,1,0,2615,2200,0,'n')", "execute"}, {"UPDATE pg_catalog.pg_depend SET deptype='n'", "prepare"}, {"DELETE FROM pg_catalog.pg_depend", "execute"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			s := fixture.ImplicitError(q+" FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}
func TestReferencePGDependDropDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.PgDependExec(t, p, b, "CREATE TABLE pgdep_drop(a INT CONSTRAINT pgdep_unique UNIQUE,id INT GENERATED ALWAYS AS IDENTITY)", "CREATE TABLE pgdep_other(a INT)")
		table := fixture.PgDependRelation(t, b, "pgdep_drop")
		seq := fixture.PgDependRelation(t, b, "pgdep_drop_id_seq")
		idx := fixture.PgDependRelation(t, b, "pgdep_unique")
		constraint := fixture.PgDependConstraint(t, b, table, "pgdep_unique")
		for _, suffix := range []string{"", " CASCADE"} {
			fixture.PgDependError(t, p, b, "DROP SEQUENCE pgdep_drop_id_seq"+suffix, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop sequence pgdep_drop_id_seq because column id of table pgdep_drop requires it", Hint: "You can drop column id of table pgdep_drop instead."})
			fixture.PgDependError(t, p, b, "DROP INDEX pgdep_unique"+suffix, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop index pgdep_unique because constraint pgdep_unique on table pgdep_drop requires it", Hint: "You can drop constraint pgdep_unique on table pgdep_drop instead."})
		}
		fixture.PgDependError(t, p, b, "ALTER SEQUENCE pgdep_drop_id_seq OWNED BY pgdep_other.a", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot change ownership of identity sequence", Detail: "Sequence \"pgdep_drop_id_seq\" is linked to table \"pgdep_drop\"."})
		fixture.PgDependBag(t, p, b, 1259, seq, fixture.PgDependEdge(1259, seq, 0, 2615, fixture.PgDependNamespace(t, b, seq), 0, "n"), fixture.PgDependEdge(1259, seq, 0, 1259, table, 2, "i"))
		fixture.PgDependBag(t, p, b, 1259, idx, fixture.PgDependEdge(1259, idx, 0, 2606, constraint, 0, "i"))
		fixture.PgDependExec(t, p, b, "DROP TABLE pgdep_drop,pgdep_other CASCADE")
		fixture.PgDependBag(t, p, b, 1259, seq)
		fixture.PgDependBag(t, p, b, 1259, idx)
	})
}
