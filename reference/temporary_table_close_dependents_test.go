package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTemporaryTableClosePermanentCheck(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE close_owner(id SERIAL)", nil)
		s.exec(s.c, "INSERT INTO close_owner DEFAULT VALUES", 1)
		s.exec(s.c, "CREATE TABLE public.close_survivor(v INT CONSTRAINT close_guard CHECK(v>0 AND nextval('close_owner_id_seq')>0))", 0)
		s.exec(s.c, "CREATE TABLE public.close_unrelated(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.close_unrelated VALUES(99)", 1)
		s.exec(s.c, "INSERT INTO public.close_survivor VALUES(7)", 1)
		s.value(s.c, "SELECT currval('close_owner_id_seq')", 2)
		temporaryTableRows(s, s.c, "public.close_survivor", 7)
		want := tempReferenceDiagnostic("23514", `new row for relation "close_survivor" violates check constraint "close_guard"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (-1).", "public", "close_survivor", "close_guard"
		failure := referenceImplicitError("INSERT INTO public.close_survivor VALUES(-1)", *want)
		failure.prepared, failure.errorStage = s.prepared, "execute"
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, failure)
		var constraintID, sequenceID int64
		for _, probe := range []struct {
			query string
			id    *int64
		}{
			{"SELECT oid FROM pg_catalog.pg_constraint WHERE conname='close_guard'", &constraintID},
			{"SELECT oid FROM pg_catalog.pg_class WHERE relname='close_owner_id_seq'", &sequenceID},
		} {
			if err := s.c.QueryRowContext(s.p.ctx, probe.query).Scan(probe.id); err != nil || *probe.id <= 0 || *probe.id > 4294967295 {
				s.t.Fatal(probe.query, *probe.id, err)
			}
		}
		backend := referenceBackend{db: s.db, recorder: s.record, pg: s.postgres}
		constraintQuery := "SELECT conname FROM pg_catalog.pg_constraint WHERE conname='close_guard'"
		constraint := referenceSelect(constraintQuery, []string{"conname"}, []string{"NAME"}, [][]any{{"close_guard"}})
		constraint.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, constraint)
		dependencyQuery := "SELECT * FROM pg_catalog.pg_depend WHERE classid=2606 AND objid=$1 AND refclassid=1259 AND refobjid=$2"
		dependency := pgDependSelect(dependencyQuery, [][]any{pgDependEdge(2606, constraintID, 0, 1259, sequenceID, 0, "n")}, constraintID, sequenceID)
		dependency.prepared = s.prepared
		pgDescriptionExecutorRows(s.t, s.p, backend, s.c, dependency)
		// A peer owns the same sequence basename; cleanup must follow provider identity.
		s.exec(s.peer, "CREATE TEMP TABLE close_owner(id SERIAL,v INT)", 0)
		s.exec(s.peer, "INSERT INTO close_owner(v) VALUES(22)", 1)
		temporaryTableRows(s, s.peer, "close_owner", 22)
		insertQuery := "INSERT INTO public.close_survivor VALUES(9)"
		insert := referenceStep{query: insertQuery, affected: 1}
		var held referenceHeldStatement
		if s.prepared {
			held = s.held(s.peer, insertQuery)
		}
		s.retire()
		constraint.rows = [][]any{}
		referenceRunStep(s.t, s.p.ctx, s.peer, s.record, constraint)
		dependency.rows = [][]any{}
		pgDescriptionExecutorRows(s.t, s.p, backend, s.peer, dependency)
		removedSequence := referenceSelect("SELECT oid FROM pg_catalog.pg_class WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, sequenceID)
		removedSequence.prepared = s.prepared
		pgDescriptionExecutorRows(s.t, s.p, backend, s.peer, removedSequence)
		temporaryTableRows(s, s.peer, "public.close_survivor", 7)
		s.exec(s.peer, "INSERT INTO public.close_survivor VALUES(-1)", 1)
		if s.prepared {
			referenceRunStep(s.t, s.p.ctx, held, s.record, insert)
		} else {
			referenceRunStep(s.t, s.p.ctx, s.peer, s.record, insert)
		}
		temporaryTableRows(s, s.peer, "public.close_survivor", -1, 7, 9)
		temporaryTableRows(s, s.peer, "public.close_unrelated", 99)
		s.exec(s.peer, "INSERT INTO public.close_unrelated VALUES(100)", 1)
		temporaryTableRows(s, s.peer, "public.close_unrelated", 99, 100)
		s.value(s.peer, "SELECT currval('close_owner_id_seq')", 1)
		s.exec(s.peer, "INSERT INTO close_owner(v) VALUES(23)", 1)
		temporaryTableRows(s, s.peer, "close_owner", 22, 23)
		s.value(s.peer, "SELECT currval('close_owner_id_seq')", 2)
		if !s.postgres {
			// Native controls are explicit API witnesses, not invented PostgreSQL calls.
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			for _, name := range []string{"close_owner", "close_guard", "pg_temp_"} {
				if strings.Contains(dump.String(), name) {
					s.t.Fatal("retired/peer TEMP provider or removed CHECK leaked", dump.String())
				}
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			temporaryTableRows(s, s.peer, "public.close_survivor", -1, 7, 9)
			temporaryTableRows(s, s.peer, "public.close_unrelated", 99, 100)
			temporaryTableRows(s, s.peer, "close_owner", 22, 23)
			s.exec(s.peer, "INSERT INTO public.close_survivor VALUES(-2)", 1)
			temporaryTableRows(s, s.peer, "public.close_survivor", -2, -1, 7, 9)
			s.exec(s.peer, "INSERT INTO close_owner(v) VALUES(24)", 1)
			temporaryTableRows(s, s.peer, "close_owner", 22, 23, 24)
			s.value(s.peer, "SELECT currval('close_owner_id_seq')", 3)
		}
	})
}
