package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferencePGDependDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		for _, tc := range []struct{ q, msg, pos string }{
			{"SELECT oid FROM pg_catalog.pg_depend", "column \"oid\" does not exist", "8"},
			{"SELECT missing FROM pg_catalog.pg_depend", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_depend", "column \"missing\" does not exist", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.msg, Position: tc.pos})
				s.selectRows = true
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("SELECT * FROM pg_catalog.pg_depend WHERE objid=$1", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		q := "SELECT * FROM pg_catalog.pg_depend WHERE FALSE"
		rows, err := b.db.QueryContext(canceled, q)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err := b.db.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled statement")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err = b.db.PrepareContext(p.ctx, q)
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
		pgDescriptionExecutorRows(t, p, b, b.db, pgDependSelect(q, [][]any{}))
		if !b.pg { // Never mutate physical PG superuser catalogs for parity.
			for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_catalog.pg_depend VALUES(1259,1,0,2615,2200,0,'n')", "execute"}, {"UPDATE pg_catalog.pg_depend SET deptype='n'", "prepare"}, {"DELETE FROM pg_catalog.pg_depend", "execute"}} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = tc.stage
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
			s := referenceImplicitError(q+" FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
	})
}
func TestReferencePGDependDropDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
		pgDependExec(t, p, b, "CREATE TABLE pgdep_drop(a INT CONSTRAINT pgdep_unique UNIQUE,id INT GENERATED ALWAYS AS IDENTITY)", "CREATE TABLE pgdep_other(a INT)")
		table := pgDependRelation(t, b, "pgdep_drop")
		seq := pgDependRelation(t, b, "pgdep_drop_id_seq")
		idx := pgDependRelation(t, b, "pgdep_unique")
		constraint := pgDependConstraint(t, b, table, "pgdep_unique")
		for _, suffix := range []string{"", " CASCADE"} {
			pgDependError(t, p, b, "DROP SEQUENCE pgdep_drop_id_seq"+suffix, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop sequence pgdep_drop_id_seq because column id of table pgdep_drop requires it", Hint: "You can drop column id of table pgdep_drop instead."})
			pgDependError(t, p, b, "DROP INDEX pgdep_unique"+suffix, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop index pgdep_unique because constraint pgdep_unique on table pgdep_drop requires it", Hint: "You can drop constraint pgdep_unique on table pgdep_drop instead."})
		}
		pgDependError(t, p, b, "ALTER SEQUENCE pgdep_drop_id_seq OWNED BY pgdep_other.a", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot change ownership of identity sequence", Detail: "Sequence \"pgdep_drop_id_seq\" is linked to table \"pgdep_drop\"."})
		pgDependBag(t, p, b, 1259, seq, pgDependEdge(1259, seq, 0, 2615, pgDependNamespace(t, b, seq), 0, "n"), pgDependEdge(1259, seq, 0, 1259, table, 2, "i"))
		pgDependBag(t, p, b, 1259, idx, pgDependEdge(1259, idx, 0, 2606, constraint, 0, "i"))
		pgDependExec(t, p, b, "DROP TABLE pgdep_drop,pgdep_other CASCADE")
		pgDependBag(t, p, b, 1259, seq)
		pgDependBag(t, p, b, 1259, idx)
	})
}
