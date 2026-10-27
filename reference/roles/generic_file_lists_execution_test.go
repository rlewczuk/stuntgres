package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func fileListHeldStep(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, step fixture.Step) {
	s.T.Helper()
	step.Prepared = false
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
}
func fileListHeldDenied(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, name string, args ...any) {
	s.T.Helper()
	step := fixture.ErrorDiagnostic(held.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name}, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	fileListHeldStep(s, held, step)
}
func fileListHeldEmpty(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, record bool, args ...any) {
	s.T.Helper()
	columns, types := fileListColumns(record)
	fileListHeldStep(s, held, fixture.Select(held.SQL, columns, types, nil, args...))
}
func TestReferenceGenericFileListsHeldACL(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		admin, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer admin.Close()
		execAdmin := func(q string) {
			if _, err := admin.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(q, err)
			}
		}
		s.Exec("CREATE TABLE gfl_sentinel(id INT)")
		s.Run(fixture.Exec("INSERT INTO gfl_sentinel VALUES(7)", 1))
		s.Exec("SET application_name='gfl_held_actor'")
		execAdmin("SET application_name='gfl_held_peer'")
		s.Exec("SET ROLE gfl_actor")
		dirname := predefinedHeld(s, "SELECT * FROM pg_ls_dir($1::text)")
		slot := predefinedHeld(s, "SELECT * FROM pg_ls_replslotdir($1::text)")
		for _, tc := range []struct {
			held            fixture.HeldStatement
			name, signature string
			record          bool
		}{{dirname, "pg_ls_dir", "pg_ls_dir(text)", false}, {slot, "pg_ls_replslotdir", "pg_ls_replslotdir(text)", true}} {
			fileListHeldDenied(s, tc.held, tc.name, nil)
			execAdmin("GRANT EXECUTE ON FUNCTION " + tc.signature + " TO gfl_actor")
			fileListHeldEmpty(s, tc.held, tc.record, nil)
			execAdmin("REVOKE EXECUTE ON FUNCTION " + tc.signature + " FROM gfl_actor")
			fileListHeldDenied(s, tc.held, tc.name, nil)
			execAdmin("GRANT EXECUTE ON FUNCTION " + tc.signature + " TO gfl_actor")
			fileListHeldEmpty(s, tc.held, tc.record, nil)
		}
		physical := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "pg_ls_dir physical directory listing is unsupported in embedded mode"}
		if s.Backend.PG {
			physical.Code = "58P01"
			physical.Message = `could not open directory "gfl_missing_497356": No such file or directory`
		}
		step := fixture.ErrorDiagnostic(dirname.SQL, physical, "gfl_missing_497356")
		step.SelectRows = true
		step.ErrorStage = "execute"
		fileListHeldStep(s, dirname, step)
		step = fixture.ErrorDiagnostic(dirname.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "path must be in or below the data directory"}, "../gfl_missing_497356")
		step.SelectRows = true
		step.ErrorStage = "execute"
		fileListHeldStep(s, dirname, step)
		monitor := predefinedHeld(s, "SELECT * FROM pg_ls_waldir()")
		fileListHeldDenied(s, monitor, "pg_ls_waldir")
		execAdmin("GRANT pg_monitor TO gfl_actor")
		if !s.Backend.PG {
			step = fixture.ErrorDiagnostic(monitor.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "pg_ls_waldir physical directory listing is unsupported in embedded mode"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			fileListHeldStep(s, monitor, step)
		} else {
			rows, err := monitor.Statement.QueryContext(s.P.Ctx)
			if err != nil {
				s.T.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				s.T.Fatal(err)
			}
		}
		execAdmin("REVOKE pg_monitor FROM gfl_actor")
		fileListHeldDenied(s, monitor, "pg_ls_waldir")
		s.Exec("RESET ROLE")
		predefinedSelect(s, "SELECT id FROM gfl_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedSelect(s, "SELECT current_setting('application_name') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"gfl_held_actor"}})
		var peer string
		if err := admin.QueryRowContext(s.P.Ctx, "SELECT current_setting('application_name')").Scan(&peer); err != nil || peer != "gfl_held_peer" {
			s.T.Fatal("peer settings changed", peer, err)
		}
	})
}
func TestReferenceGenericFileListsPhysicalBoundary(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE gfl_sentinel(id INT)")
		s.Run(fixture.Exec("INSERT INTO gfl_sentinel VALUES(7)", 1))
		var actor, log string
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_user,current_setting('log_directory')").Scan(&actor, &log); err != nil {
			s.T.Fatal(err)
		}
		fileListMissing(s, "SELECT * FROM pg_ls_dir('gfl_missing_497356')", "gfl_missing_497356", false)
		for _, missing := range []bool{false, true} {
			for _, dot := range []bool{false, true} {
				fileListMissing(s, fmt.Sprintf("SELECT * FROM pg_ls_dir('gfl_missing_497356',%t,%t)", missing, dot), "gfl_missing_497356", missing)
			}
		}
		q := "SELECT * FROM pg_ls_dir('PG_VERSION')"
		if s.Backend.PG {
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42809", Message: `could not open directory "PG_VERSION": Not a directory`}, true, "execute")
		} else {
			fileListUnsupported(s, q, "pg_ls_dir")
		}
		for _, slot := range []string{"gfl_absent_slot", "", "UPPER", "a/b", strings.Repeat("x", 80)} {
			q = "SELECT * FROM pg_ls_replslotdir(" + fileListLiteral(slot) + ")"
			if s.Backend.PG {
				predefinedFailure(s, q, fixture.Diagnostic{Code: "42704", Message: fmt.Sprintf("replication slot %q does not exist", slot)}, true, "execute")
			} else {
				fileListUnsupported(s, q, "pg_ls_replslotdir")
			}
		}
		for _, sig := range fileListSignatures[2:] {
			if sig.args == "" {
				fileListStockRows(s, sig.name)
			}
		}
		for _, dot := range []bool{false, true} {
			q = fmt.Sprintf("SELECT * FROM pg_ls_dir('.',false,%t)", dot)
			if !s.Backend.PG {
				fileListUnsupported(s, q, "pg_ls_dir")
				continue
			}
			rows, err := s.C.QueryContext(s.P.Ctx, q)
			if err != nil {
				s.T.Fatal(err)
			}
			bag := map[string]int{}
			for rows.Next() {
				var value string
				if err := rows.Scan(&value); err != nil {
					rows.Close()
					s.T.Fatal(err)
				}
				bag[value]++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				s.T.Fatal(err)
			}
			dots := 0
			if dot {
				dots = 1
			}
			if bag["PG_VERSION"] != 1 || bag["."] != dots || bag[".."] != dots {
				s.T.Fatal("directory entry semantics", bag)
			}
		}
		predefinedSelect(s, "SELECT id FROM gfl_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedSelect(s, "SELECT current_user::text AS actor,current_setting('log_directory') AS log", []string{"actor", "log"}, []string{"TEXT", "TEXT"}, [][]any{{actor, log}})
	})
}
func TestReferenceGenericFileListsFeatureSmoke(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE gfl_sentinel(id INT)")
		s.Run(fixture.Exec("INSERT INTO gfl_sentinel VALUES(7)", 1))
		admin, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer admin.Close()
		grant := func(q string) {
			if _, err := admin.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec("SET ROLE gfl_actor")
		held := predefinedHeld(s, "SELECT * FROM pg_ls_dir($1::text)")
		fileListHeldDenied(s, held, "pg_ls_dir", nil)
		grant("GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_actor")
		fileListHeldEmpty(s, held, false, nil)
		d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "pg_ls_dir physical directory listing is unsupported in embedded mode"}
		if s.Backend.PG {
			d.Code = "58P01"
			d.Message = `could not open directory "gfl_missing_497356": No such file or directory`
		}
		step := fixture.ErrorDiagnostic(held.SQL, d, "gfl_missing_497356")
		step.SelectRows = true
		step.ErrorStage = "execute"
		fileListHeldStep(s, held, step)
		step = fixture.ErrorDiagnostic(held.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "path must be in or below the data directory"}, "../gfl_missing")
		step.SelectRows = true
		step.ErrorStage = "execute"
		fileListHeldStep(s, held, step)
		fileListDenied(s, "pg_ls_dir(NULL::text,false,false)", "pg_ls_dir")
		grant("GRANT EXECUTE ON FUNCTION pg_ls_dir(text,boolean,boolean) TO gfl_actor")
		fileListEmpty(s, "pg_ls_dir(NULL::text,false,false)", false)
		fileListEmpty(s, "pg_ls_dir('.',NULL::boolean,false)", false)
		grant("GRANT pg_monitor TO gfl_actor")
		predefinedSelect(s, "SELECT * FROM pg_ls_replslotdir(NULL::text) WITH ORDINALITY", []string{"name", "size", "modification", "ordinality"}, []string{"TEXT", "INT8", "TIMESTAMPTZ", "INT8"}, nil)
		q := "SELECT * FROM pg_ls_replslotdir('gfl_absent_slot')"
		if s.Backend.PG {
			predefinedFailure(s, q, fixture.Diagnostic{Code: "42704", Message: `replication slot "gfl_absent_slot" does not exist`}, true, "execute")
		} else {
			fileListUnsupported(s, q, "pg_ls_replslotdir")
		}
		grant("REVOKE EXECUTE ON FUNCTION pg_ls_dir(text) FROM gfl_actor")
		fileListHeldDenied(s, held, "pg_ls_dir", nil)
		s.Exec("RESET ROLE")
		if !s.Backend.PG {
			fileListControlRoundtrip(s)
		}
	})
}
