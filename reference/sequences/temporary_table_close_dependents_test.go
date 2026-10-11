package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableClosePermanentCheck(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE close_owner(id SERIAL)", nil)
		s.Exec(s.C, "INSERT INTO close_owner DEFAULT VALUES", 1)
		s.Exec(s.C, "CREATE TABLE public.close_survivor(v INT CONSTRAINT close_guard CHECK(v>0 AND nextval('close_owner_id_seq')>0))", 0)
		s.Exec(s.C, "CREATE TABLE public.close_unrelated(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.close_unrelated VALUES(99)", 1)
		s.Exec(s.C, "INSERT INTO public.close_survivor VALUES(7)", 1)
		s.Value(s.C, "SELECT currval('close_owner_id_seq')", 2)
		fixture.TemporaryTableRows(s, s.C, "public.close_survivor", 7)
		want := fixture.TempReferenceDiagnostic("23514", `new row for relation "close_survivor" violates check constraint "close_guard"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (-1).", "public", "close_survivor", "close_guard"
		failure := fixture.ImplicitError("INSERT INTO public.close_survivor VALUES(-1)", *want)
		failure.Prepared, failure.ErrorStage = s.Prepared, "execute"
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, failure)
		var constraintID, sequenceID int64
		for _, probe := range []struct {
			query string
			id    *int64
		}{
			{"SELECT oid FROM pg_catalog.pg_constraint WHERE conname='close_guard'", &constraintID},
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='close_owner_id_seq'", &sequenceID},
		} {
			if err := s.C.QueryRowContext(s.P.Ctx, probe.query).Scan(probe.id); err != nil || *probe.id <= 0 || *probe.id > 4294967295 {
				s.T.Fatal(probe.query, *probe.id, err)
			}
		}
		backend := fixture.Backend{DB: s.DB, Recorder: s.Record, PG: s.Postgres}
		constraintQuery := "SELECT conname FROM pg_catalog.pg_constraint WHERE conname='close_guard'"
		constraint := fixture.Select(constraintQuery, []string{"conname"}, []string{"NAME"}, [][]any{{"close_guard"}})
		constraint.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, constraint)
		dependencyQuery := "SELECT * FROM pg_catalog.pg_depend WHERE classid=2606 AND objid=$1 AND refclassid=1259 AND refobjid=$2"
		dependency := fixture.PgDependSelect(dependencyQuery, [][]any{fixture.PgDependEdge(2606, constraintID, 0, 1259, sequenceID, 0, "n")}, constraintID, sequenceID)
		dependency.Prepared = s.Prepared
		fixture.PgDescriptionExecutorRows(s.T, s.P, backend, s.C, dependency)
		// A peer owns the same sequence basename; cleanup must follow provider identity.
		s.Exec(s.Peer, "CREATE TEMP TABLE close_owner(id SERIAL,v INT)", 0)
		s.Exec(s.Peer, "INSERT INTO close_owner(v) VALUES(22)", 1)
		fixture.TemporaryTableRows(s, s.Peer, "close_owner", 22)
		insertQuery := "INSERT INTO public.close_survivor VALUES(9)"
		insert := fixture.Step{Query: insertQuery, Affected: 1}
		var held fixture.HeldStatement
		if s.Prepared {
			held = s.Held(s.Peer, insertQuery)
		}
		s.Retire()
		constraint.Rows = [][]any{}
		fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, constraint)
		dependency.Rows = [][]any{}
		fixture.PgDescriptionExecutorRows(s.T, s.P, backend, s.Peer, dependency)
		removedSequence := fixture.Select("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, sequenceID)
		removedSequence.Prepared = s.Prepared
		fixture.PgDescriptionExecutorRows(s.T, s.P, backend, s.Peer, removedSequence)
		fixture.TemporaryTableRows(s, s.Peer, "public.close_survivor", 7)
		s.Exec(s.Peer, "INSERT INTO public.close_survivor VALUES(-1)", 1)
		if s.Prepared {
			fixture.RunStep(s.T, s.P.Ctx, held, s.Record, insert)
		} else {
			fixture.RunStep(s.T, s.P.Ctx, s.Peer, s.Record, insert)
		}
		fixture.TemporaryTableRows(s, s.Peer, "public.close_survivor", -1, 7, 9)
		fixture.TemporaryTableRows(s, s.Peer, "public.close_unrelated", 99)
		s.Exec(s.Peer, "INSERT INTO public.close_unrelated VALUES(100)", 1)
		fixture.TemporaryTableRows(s, s.Peer, "public.close_unrelated", 99, 100)
		s.Value(s.Peer, "SELECT currval('close_owner_id_seq')", 1)
		s.Exec(s.Peer, "INSERT INTO close_owner(v) VALUES(23)", 1)
		fixture.TemporaryTableRows(s, s.Peer, "close_owner", 22, 23)
		s.Value(s.Peer, "SELECT currval('close_owner_id_seq')", 2)
		if !s.Postgres {
			// Native controls are explicit API witnesses, not invented PostgreSQL calls.
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			for _, name := range []string{"close_owner", "close_guard", "pg_temp_"} {
				if strings.Contains(dump.String(), name) {
					s.T.Fatal("retired/peer TEMP provider or removed CHECK leaked", dump.String())
				}
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			fixture.TemporaryTableRows(s, s.Peer, "public.close_survivor", -1, 7, 9)
			fixture.TemporaryTableRows(s, s.Peer, "public.close_unrelated", 99, 100)
			fixture.TemporaryTableRows(s, s.Peer, "close_owner", 22, 23)
			s.Exec(s.Peer, "INSERT INTO public.close_survivor VALUES(-2)", 1)
			fixture.TemporaryTableRows(s, s.Peer, "public.close_survivor", -2, -1, 7, 9)
			s.Exec(s.Peer, "INSERT INTO close_owner(v) VALUES(24)", 1)
			fixture.TemporaryTableRows(s, s.Peer, "close_owner", 22, 23, 24)
			s.Value(s.Peer, "SELECT currval('close_owner_id_seq')", 3)
		}
	})
}
