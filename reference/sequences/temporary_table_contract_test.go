package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableDiagnostics(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		for _, c := range []struct{ q, code, message, position string }{
			{"CREATE TEMP TABLE public.bad(v INT)", "42P16", "cannot create temporary relation in non-temporary schema", "19"},
			{"CREATE TABLE bad(v INT) ON COMMIT PRESERVE ROWS", "42P16", "ON COMMIT can only be used on temporary tables", ""},
			{"CREATE TEMP TABLE absent_schema.bad(v INT)", "3F000", `schema "absent_schema" does not exist`, "19"},
		} {
			s.Utility(c.q, fixture.TempReferenceDiagnostic(c.code, c.message, "", c.position))
		}
		s.Utility("CREATE TEMP TABLE diagnostic(v INT)", nil)
		s.Utility("CREATE TEMP TABLE diagnostic(v INT)", fixture.TempReferenceDiagnostic("42P07", `relation "diagnostic" already exists`, "", ""))
		s.Exec(s.C, "INSERT INTO diagnostic VALUES(11)", 1)
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Step{Query: "CREATE TEMP TABLE IF NOT EXISTS diagnostic(other TEXT) ON COMMIT DROP", Prepared: s.Prepared})
		fixture.TemporaryTableRows(s, s.C, "diagnostic", 11)
		s.Utility("DROP TABLE pg_temp.missing_temp", fixture.TempReferenceDiagnostic("42P01", `table "missing_temp" does not exist`, "", ""))
		s.Missing(s.C, "missing_temp", "v", "15")
		for _, c := range []struct{ q, message, position string }{
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT BANANA", `syntax error at or near "BANANA"`, "40"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DELETE", `syntax error at end of input`, "46"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DELETE RANGE", `syntax error at or near "RANGE"`, "47"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT PRESERVE GROUPS", `syntax error at or near "GROUPS"`, "49"},
			{"CREATE TEMP TABLE bad(v INT) ON COMMIT DROP garbage", `syntax error at or near "garbage"`, "45"},
		} {
			d := fixture.TempReferenceDiagnostic("42601", c.message, "", c.position)
			stage := "execute"
			if s.Prepared {
				stage = "prepare"
			}
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Step{Query: c.q, Prepared: s.Prepared, State: d.Code, Diagnostic: d, ErrorStage: stage})
		}
		s.Exec(s.Peer, "CREATE TEMP TABLE foreign_owner(v INT)", 0)
		s.Exec(s.Peer, "INSERT INTO foreign_owner VALUES(99)", 1)
		s.Exec(s.Peer, "SET search_path TO pg_temp", 0)
		foreign := fixture.TempReferenceNamespace(s.T, s, true)
		s.Utility("CREATE TABLE "+foreign+".bad(v INT)", fixture.TempReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions", "", "14"))
		q := "SELECT v FROM " + foreign + ".foreign_owner"
		if s.Postgres {
			// Pinned PostgreSQL returns an empty cursor for this small peer TEMP
			// relation, independently of the owner's populated local-buffer rows.
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.SequenceInt64Step(fixture.Step{Query: q, Prepared: s.Prepared, SelectRows: true, Columns: []string{"v"}, Types: []string{"INT4"}, Rows: [][]any{}}))
		} else {
			d := fixture.TempReferenceDiagnostic("42501", "permission denied for schema "+foreign, "", "")
			stage := "execute"
			if s.Prepared {
				stage = "prepare"
			}
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Step{Query: q, Prepared: s.Prepared, SelectRows: true, State: d.Code, Diagnostic: d, ErrorStage: stage})
		}
	})
}

func temporaryTableDependencies(s *fixture.TempReferenceSession) {
	// REL_18_0 parse_utilcmd.c generateSerialExtraStmts rejects an explicit
	// persistence override for a temporary parent at the option token.
	s.Utility("CREATE TEMP TABLE identity_logged(v INT GENERATED ALWAYS AS IDENTITY (LOGGED))", fixture.TempReferenceDiagnostic("42P16", "cannot set logged status of a temporary sequence", "", "71"))
	s.Utility("CREATE TEMP TABLE identity_unlogged(v INT GENERATED ALWAYS AS IDENTITY (UNLOGGED))", fixture.TempReferenceDiagnostic("42P16", "cannot set logged status of a temporary sequence", "", "73"))
	s.Missing(s.C, "identity_logged", "v", "15")
	s.Missing(s.C, "identity_unlogged", "v", "15")
	s.Utility("CREATE TEMP TABLE dependencies(id SERIAL UNIQUE,v INT GENERATED ALWAYS AS IDENTITY) ON COMMIT DELETE ROWS", nil)
	s.Exec(s.C, "CREATE INDEX dependency_plain ON dependencies(id)", 0)
	s.Exec(s.C, "CREATE INDEX dependency_expression ON dependencies((id + 1))", 0)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO dependencies DEFAULT VALUES", 1)
	fixture.TemporaryTableRows(s, s.C, "dependencies", 1)
	s.Exec(s.C, "COMMIT", 0)
	fixture.TemporaryTableRows(s, s.C, "dependencies")
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO dependencies DEFAULT VALUES", 1)
	fixture.TemporaryTableRows(s, s.C, "dependencies", 2)
	s.Exec(s.C, "ROLLBACK", 0)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO dependencies DEFAULT VALUES", 1)
	fixture.TemporaryTableRows(s, s.C, "dependencies", 3)
	s.Exec(s.C, "COMMIT", 0)
	s.Utility("CREATE TEMP TABLE readonly_temp(v INT)", nil)
	s.Exec(s.C, "BEGIN READ ONLY", 0)
	s.Exec(s.C, "INSERT INTO readonly_temp VALUES(7)", 1)
	s.Exec(s.C, "UPDATE readonly_temp SET v=8", 1)
	s.Exec(s.C, "DELETE FROM readonly_temp", 1)
	s.Exec(s.C, "COMMIT", 0)
	s.Exec(s.C, "BEGIN READ ONLY", 0)
	s.Utility("CREATE TEMP TABLE readonly_bad(v INT)", fixture.TempReferenceDiagnostic("25006", "cannot execute CREATE TABLE in a read-only transaction", "", ""))
	s.Exec(s.C, "ROLLBACK", 0)
	s.Utility("CREATE TABLE readonly_public(v INT)", nil)
	s.Exec(s.C, "BEGIN READ ONLY", 0)
	step := fixture.ImplicitError("INSERT INTO readonly_public VALUES(7)", *fixture.TempReferenceDiagnostic("25006", "cannot execute INSERT in a read-only transaction", "", ""))
	step.Prepared, step.ErrorStage = s.Prepared, "execute"
	fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
	s.Exec(s.C, "ROLLBACK", 0)
	s.Exec(s.C, "BEGIN READ ONLY", 0)
	s.Utility("DROP TABLE readonly_temp", fixture.TempReferenceDiagnostic("25006", "cannot execute DROP TABLE in a read-only transaction", "", ""))
	s.Exec(s.C, "ROLLBACK", 0)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO readonly_temp VALUES(9)", 1)
	s.Utility("PREPARE TRANSACTION 'temporary_table_forbidden'", fixture.TempReferenceDiagnostic("0A000", "cannot PREPARE a transaction that has operated on temporary objects", "", ""))
	s.Exec(s.C, "ROLLBACK", 0)
	fixture.TemporaryTableRows(s, s.C, "readonly_temp")
	s.Exec(s.C, "BEGIN", 0)
	s.Utility("CREATE TEMP TABLE drop_dependencies(id SERIAL UNIQUE,v INT GENERATED ALWAYS AS IDENTITY) ON COMMIT DROP", nil)
	s.Exec(s.C, "INSERT INTO drop_dependencies DEFAULT VALUES", 1)
	s.Exec(s.C, "COMMIT", 0)
	s.Missing(s.C, "drop_dependencies", "v", "15")
	s.Missing(s.C, "drop_dependencies_id_seq", "last_value", "24")
	s.Missing(s.C, "drop_dependencies_v_seq", "last_value", "24")
}

func TestReferenceTemporaryTableDependencies(t *testing.T) {
	fixture.TemporaryTableScenario(t, temporaryTableDependencies)
}

func temporaryTableControls(s *fixture.TempReferenceSession) {
	temporaryTableIsolation(s)
	if s.Postgres {
		return
	} // Native persistence controls have no PostgreSQL API equivalent.
	s.Utility("CREATE TEMP TABLE control_delete(v INT) ON COMMIT DELETE ROWS", nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
		s.T.Fatal(err)
	}
	text := dump.String()
	for _, forbidden := range []string{"pg_temp_", "VALUES (11)", "VALUES (22)"} {
		if strings.Contains(text, forbidden) {
			s.T.Fatalf("temporary contents in persistent dump: %s", text)
		}
	}
	if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
		s.T.Fatal(err)
	}
	fixture.TemporaryTableRows(s, s.C, "isolated", 11)
	fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("CREATE TABLE public.isolated(v INT); INSERT INTO public.isolated VALUES(9);")); err != nil {
		s.T.Fatal(err)
	}
	fixture.TemporaryTableRows(s, s.C, "isolated", 11)
	fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
	fixture.TemporaryTableRows(s, s.C, "public.isolated", 9)
	for _, text := range []string{"CREATE TEMP TABLE forbidden(v INT);", "CREATE TABLE pg_temp.forbidden(v INT);"} {
		err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(text))
		want := fixture.ControlsReferenceDiagnostic("0A000", "restore cannot stage non-retractable effects")
		fixture.AssertDiagnostic(s.T, err, &want)
	}
	canceled, cancel := context.WithCancel(s.P.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, s.P.Native, bytes.NewReader(dump.Bytes())); err == nil {
		s.T.Fatal("canceled restore succeeded")
	}
	fixture.TemporaryTableRows(s, s.C, "isolated", 11)
	fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
	fixture.TemporaryTableRows(s, s.C, "public.isolated", 9)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO control_delete VALUES(44)", 1)
	fixture.TemporaryTableRows(s, s.C, "control_delete", 44)
	s.Exec(s.C, "COMMIT", 0)
	fixture.TemporaryTableRows(s, s.C, "control_delete")
}

func TestReferenceTemporaryTableControls(t *testing.T) {
	fixture.TemporaryTableScenario(t, temporaryTableControls)
}

func TestReferenceTemporaryTableFeatureSmoke(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		temporaryTableControls(s)
		temporaryTableCommit(s)
		temporaryTableDependencies(s)
		s.Exec(s.C, "BEGIN", 0)
		s.Utility("CREATE TEMP TABLE smoke_rollback(v INT)", nil)
		s.Exec(s.C, "ROLLBACK", 0)
		s.Missing(s.C, "smoke_rollback", "v", "15")
		s.Retire()
		fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
		if s.Postgres {
			fixture.TemporaryTableRows(s, s.Peer, "public.isolated", 7)
		} else {
			fixture.TemporaryTableRows(s, s.Peer, "public.isolated", 9)
		}
	})
}
