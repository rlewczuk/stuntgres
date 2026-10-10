package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTemporaryTableDiagnostics(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		for _, c := range []struct{ q, code, message, position string }{
			{"CREATE TEMP TABLE public.bad(v INT)", "42P16", "cannot create temporary relation in non-temporary schema", "19"},
			{"CREATE TABLE bad(v INT) ON COMMIT PRESERVE ROWS", "42P16", "ON COMMIT can only be used on temporary tables", ""},
			{"CREATE TEMP TABLE absent_schema.bad(v INT)", "3F000", `schema "absent_schema" does not exist`, "19"},
		} {
			s.utility(c.q, tempReferenceDiagnostic(c.code, c.message, "", c.position))
		}
		s.utility("CREATE TEMP TABLE diagnostic(v INT)", nil)
		s.utility("CREATE TEMP TABLE diagnostic(v INT)", tempReferenceDiagnostic("42P07", `relation "diagnostic" already exists`, "", ""))
		s.exec(s.c, "INSERT INTO diagnostic VALUES(11)", 1)
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, referenceStep{query: "CREATE TEMP TABLE IF NOT EXISTS diagnostic(other TEXT) ON COMMIT DROP", prepared: s.prepared})
		temporaryTableRows(s, s.c, "diagnostic", 11)
		s.utility("DROP TABLE pg_temp.missing_temp", tempReferenceDiagnostic("42P01", `table "missing_temp" does not exist`, "", ""))
		s.missing(s.c, "missing_temp", "v", "15")
		for _, c := range []struct{ q, message, position string }{
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT BANANA", `syntax error at or near "BANANA"`, "40"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DELETE", `syntax error at end of input`, "46"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DELETE RANGE", `syntax error at or near "RANGE"`, "47"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT PRESERVE GROUPS", `syntax error at or near "GROUPS"`, "49"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DROP garbage", `syntax error at or near "garbage"`, "45"},
		} {
			d := tempReferenceDiagnostic("42601", c.message, "", c.position)
			stage := "execute"
			if s.prepared {
				stage = "prepare"
			}
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, referenceStep{query: c.q, prepared: s.prepared, state: d.Code, diagnostic: d, errorStage: stage})
		}
		s.exec(s.peer, "CREATE TEMP TABLE foreign_owner(v INT)", 0)
		s.exec(s.peer, "INSERT INTO foreign_owner VALUES(99)", 1)
		s.exec(s.peer, "SET search_path TO pg_temp", 0)
		foreign := tempReferenceNamespace(s.t, s, true)
		s.utility("CREATE TABLE "+foreign+".bad(v INT)", tempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", "14"))
		q := "SELECT v FROM " + foreign + ".foreign_owner"
		if s.postgres {
			// Pinned PostgreSQL returns an empty cursor for this small peer TEMP
			// relation, independently of the owner's populated local-buffer rows.
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, sequenceInt64Step(referenceStep{query: q, prepared: s.prepared, selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: [][]any{}}))
		} else {
			d := tempReferenceDiagnostic("42501", "permission denied for schema "+foreign, "", "")
			stage := "execute"
			if s.prepared {
				stage = "prepare"
			}
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, referenceStep{query: q, prepared: s.prepared, selectRows: true, state: d.Code, diagnostic: d, errorStage: stage})
		}
	})
}

func temporaryTableDependencies(s *tempReferenceSession) {
	// REL_18_0 parse_utilcmd.c generateSerialExtraStmts rejects an explicit
	// persistence override for a temporary parent at the option token.
	s.utility("CREATE TEMP TABLE identity_logged(v INT GENERATED ALWAYS AS IDENTITY (LOGGED))", tempReferenceDiagnostic("42P16", "cannot set logged status of a temporary sequence", "", "71"))
	s.utility("CREATE TEMP TABLE identity_unlogged(v INT GENERATED ALWAYS AS IDENTITY (UNLOGGED))", tempReferenceDiagnostic("42P16", "cannot set logged status of a temporary sequence", "", "73"))
	s.missing(s.c, "identity_logged", "v", "15")
	s.missing(s.c, "identity_unlogged", "v", "15")
	s.utility("CREATE TEMP TABLE dependencies(id SERIAL UNIQUE,v INT GENERATED ALWAYS AS IDENTITY) ON COMMIT DELETE ROWS", nil)
	s.exec(s.c, "CREATE INDEX dependency_plain ON dependencies(id)", 0)
	s.exec(s.c, "CREATE INDEX dependency_expression ON dependencies((id + 1))", 0)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO dependencies DEFAULT VALUES", 1)
	temporaryTableRows(s, s.c, "dependencies", 1)
	s.exec(s.c, "COMMIT", 0)
	temporaryTableRows(s, s.c, "dependencies")
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO dependencies DEFAULT VALUES", 1)
	temporaryTableRows(s, s.c, "dependencies", 2)
	s.exec(s.c, "ROLLBACK", 0)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO dependencies DEFAULT VALUES", 1)
	temporaryTableRows(s, s.c, "dependencies", 3)
	s.exec(s.c, "COMMIT", 0)
	s.utility("CREATE TEMP TABLE readonly_temp(v INT)", nil)
	s.exec(s.c, "BEGIN READ ONLY", 0)
	s.exec(s.c, "INSERT INTO readonly_temp VALUES(7)", 1)
	s.exec(s.c, "UPDATE readonly_temp SET v=8", 1)
	s.exec(s.c, "DELETE FROM readonly_temp", 1)
	s.exec(s.c, "COMMIT", 0)
	s.exec(s.c, "BEGIN READ ONLY", 0)
	s.utility("CREATE TEMP TABLE readonly_bad(v INT)", tempReferenceDiagnostic("25006", "cannot execute CREATE TABLE in a read-only transaction", "", ""))
	s.exec(s.c, "ROLLBACK", 0)
	s.utility("CREATE TABLE readonly_public(v INT)", nil)
	s.exec(s.c, "BEGIN READ ONLY", 0)
	step := referenceImplicitError("INSERT INTO readonly_public VALUES(7)", *tempReferenceDiagnostic("25006", "cannot execute INSERT in a read-only transaction", "", ""))
	step.prepared, step.errorStage = s.prepared, "execute"
	referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
	s.exec(s.c, "ROLLBACK", 0)
	s.exec(s.c, "BEGIN READ ONLY", 0)
	s.utility("DROP TABLE readonly_temp", tempReferenceDiagnostic("25006", "cannot execute DROP TABLE in a read-only transaction", "", ""))
	s.exec(s.c, "ROLLBACK", 0)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO readonly_temp VALUES(9)", 1)
	s.utility("PREPARE TRANSACTION 'temporary_table_forbidden'", tempReferenceDiagnostic("0A000", "cannot PREPARE a transaction that has operated on temporary objects", "", ""))
	s.exec(s.c, "ROLLBACK", 0)
	temporaryTableRows(s, s.c, "readonly_temp")
	s.exec(s.c, "BEGIN", 0)
	s.utility("CREATE TEMP TABLE drop_dependencies(id SERIAL UNIQUE,v INT GENERATED ALWAYS AS IDENTITY) ON COMMIT DROP", nil)
	s.exec(s.c, "INSERT INTO drop_dependencies DEFAULT VALUES", 1)
	s.exec(s.c, "COMMIT", 0)
	s.missing(s.c, "drop_dependencies", "v", "15")
	s.missing(s.c, "drop_dependencies_id_seq", "last_value", "24")
	s.missing(s.c, "drop_dependencies_v_seq", "last_value", "24")
}

func TestReferenceTemporaryTableDependencies(t *testing.T) {
	temporaryTableScenario(t, temporaryTableDependencies)
}

func temporaryTableControls(s *tempReferenceSession) {
	temporaryTableIsolation(s)
	if s.postgres {
		return
	} // Native persistence controls have no PostgreSQL API equivalent.
	s.utility("CREATE TEMP TABLE control_delete(v INT) ON COMMIT DELETE ROWS", nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
		s.t.Fatal(err)
	}
	text := dump.String()
	for _, forbidden := range []string{"pg_temp_", "VALUES (11)", "VALUES (22)"} {
		if strings.Contains(text, forbidden) {
			s.t.Fatalf("temporary contents in persistent dump: %s", text)
		}
	}
	if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
		s.t.Fatal(err)
	}
	temporaryTableRows(s, s.c, "isolated", 11)
	temporaryTableRows(s, s.peer, "isolated", 22)
	if err := stuntgres.Restore(s.p.ctx, s.p.native, strings.NewReader("CREATE TABLE public.isolated(v INT); INSERT INTO public.isolated VALUES(9);")); err != nil {
		s.t.Fatal(err)
	}
	temporaryTableRows(s, s.c, "isolated", 11)
	temporaryTableRows(s, s.peer, "isolated", 22)
	temporaryTableRows(s, s.c, "public.isolated", 9)
	for _, text := range []string{"CREATE TEMP TABLE forbidden(v INT);", "CREATE TABLE pg_temp.forbidden(v INT);"} {
		err := stuntgres.Restore(s.p.ctx, s.p.native, strings.NewReader(text))
		want := controlsReferenceDiagnostic("0A000", "restore cannot stage non-retractable effects")
		referenceAssertDiagnostic(s.t, err, &want)
	}
	canceled, cancel := context.WithCancel(s.p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, s.p.native, bytes.NewReader(dump.Bytes())); err == nil {
		s.t.Fatal("canceled restore succeeded")
	}
	temporaryTableRows(s, s.c, "isolated", 11)
	temporaryTableRows(s, s.peer, "isolated", 22)
	temporaryTableRows(s, s.c, "public.isolated", 9)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO control_delete VALUES(44)", 1)
	temporaryTableRows(s, s.c, "control_delete", 44)
	s.exec(s.c, "COMMIT", 0)
	temporaryTableRows(s, s.c, "control_delete")
}

func TestReferenceTemporaryTableControls(t *testing.T) {
	temporaryTableScenario(t, temporaryTableControls)
}

func TestReferenceTemporaryTableFeatureSmoke(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		temporaryTableControls(s)
		temporaryTableCommit(s)
		temporaryTableDependencies(s)
		s.exec(s.c, "BEGIN", 0)
		s.utility("CREATE TEMP TABLE smoke_rollback(v INT)", nil)
		s.exec(s.c, "ROLLBACK", 0)
		s.missing(s.c, "smoke_rollback", "v", "15")
		s.retire()
		temporaryTableRows(s, s.peer, "isolated", 22)
		if s.postgres {
			temporaryTableRows(s, s.peer, "public.isolated", 7)
		} else {
			temporaryTableRows(s, s.peer, "public.isolated", 9)
		}
	})
}
