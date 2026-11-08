package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func tempAsAdmissionScenario(t *testing.T, body func(*fixture.TempReferenceSession, *[]fixture.Diagnostic)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					peer, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					s := &fixture.TempReferenceSession{T: t, P: p, DB: db, C: c, Peer: peer, Record: record, Prepared: prepared, Postgres: b.PG}
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					body(s, &notices)
				})
			}
		})
	}
}

func tempAsAdmissionQuery(prefix, name string, noData bool) string {
	q := prefix + " " + name + " AS SELECT 7::integer AS v"
	if noData {
		q += " WITH NO DATA"
	}
	return q
}

func tempAsAdmissionCount(noData bool) int64 {
	if noData {
		return 0
	}
	return 1
}

func tempAsAdmissionRows(s *fixture.TempReferenceSession, name string, noData bool) {
	s.T.Helper()
	if noData {
		tempAsRows(s, s.C, name)
	} else {
		tempAsRows(s, s.C, name, 7)
	}
	tempAsRun(s, s.C, fixture.Select("SELECT relpersistence::text AS p FROM pg_class WHERE oid='"+name+"'::regclass", []string{"p"}, []string{"TEXT"}, [][]any{{"t"}}))
}

func tempAsAdmissionGlobal(s *fixture.TempReferenceSession, c fixture.Executor, q string) {
	s.T.Helper()
	tempAsError(s, c, q, "22023", "only shared relations can be placed in pg_global tablespace", "", false)
}

func tempAsAdmissionSettings(s *fixture.TempReferenceSession) {
	s.T.Helper()
	var defaults, temporary string
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_setting('default_tablespace'),current_setting('temp_tablespaces')").Scan(&defaults, &temporary); err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() {
		s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		s.C.ExecContext(s.P.Ctx, "SELECT set_config('default_tablespace',$1,false)", defaults)
		s.C.ExecContext(s.P.Ctx, "SELECT set_config('temp_tablespaces',$1,false)", temporary)
	})
	tempAsExec(s, s.C, "RESET default_tablespace", 0)
	tempAsExec(s, s.C, "RESET temp_tablespaces", 0)
}

func tempAsAdmissionPolicy(s *fixture.TempReferenceSession, want string) {
	s.T.Helper()
	tempAsRun(s, s.C, fixture.Select("SELECT current_setting('temp_tablespaces') AS policy", []string{"policy"}, []string{"TEXT"}, [][]any{{want}}))
}

func tempAsAdmissionAbsent(s *fixture.TempReferenceSession, name string) {
	s.T.Helper()
	parts := strings.Split(name, ".")
	tempAsAbsent(s, parts[len(parts)-1])
}

func tempAsAdmissionSentinel(s *fixture.TempReferenceSession) {
	s.T.Helper()
	tempAsRun(s, s.C, fixture.Select("SELECT * FROM ta_space_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
}

func tempAsAdmissionNotice(s *fixture.TempReferenceSession, notices *[]fixture.Diagnostic, name, q string) {
	s.T.Helper()
	*notices = nil
	if s.Prepared {
		stmt, err := s.C.PrepareContext(s.P.Ctx, q)
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		if len(*notices) != 0 {
			s.T.Fatal("Prepare emitted a skip notice", *notices)
		}
		fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}, s.Record, fixture.Exec(q, 0))
	} else {
		tempAsExec(s, s.C, q, 0)
	}
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "` + name + `" already exists, skipping`}
	if len(*notices) != 1 || (*notices)[0] != want {
		s.T.Fatalf("skip notices = %#v, want %#v", *notices, want)
	}
	*notices = nil
}

func tempAsAdmissionDefault(s *fixture.TempReferenceSession, _ *[]fixture.Diagnostic, prefix, name string, noData bool) {
	tempAsAdmissionSettings(s)
	tempAsExec(s, s.C, "CREATE TABLE public.ta_space_source(v INT)", 0)
	tempAsExec(s, s.C, "INSERT INTO public.ta_space_source VALUES(73)", 1)
	tempAsExec(s, s.C, "SET default_tablespace='pg_global'", 0)
	tempAsExec(s, s.C, tempAsAdmissionQuery(prefix, name, noData), tempAsAdmissionCount(noData))
	tempAsAdmissionRows(s, name, noData)
	unqualified := name
	if prefix == "CREATE TABLE" {
		unqualified = "ta_space_alias"
	}
	s.Missing(s.Peer, unqualified, "v", "15")
	tempAsRows(s, s.C, "public.ta_space_source", 73)
}

func tempAsAdmissionTempGlobal(s *fixture.TempReferenceSession, notices *[]fixture.Diagnostic, prefix, name string, noData bool) {
	tempAsAdmissionSettings(s)
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_space_sentinel ON COMMIT PRESERVE ROWS AS SELECT 73::integer AS v", 1)
	tempAsExec(s, s.C, "CREATE SEQUENCE ta_space_draw START 10", 0)
	q := tempAsAdmissionQuery(prefix, name, noData)
	for _, defaultPolicy := range []string{"", "pg_global"} {
		tempAsExec(s, s.C, "SET default_tablespace='"+defaultPolicy+"'", 0)
		tempAsExec(s, s.C, "SET temp_tablespaces='pg_global'", 0)
		tempAsAdmissionGlobal(s, s.C, q)
		tempAsAdmissionAbsent(s, name)
		tempAsAdmissionSentinel(s)
	}
	skipPrefix := "CREATE TEMP TABLE IF NOT EXISTS ta_space_sentinel"
	if prefix == "CREATE TABLE" {
		skipPrefix = "CREATE TABLE IF NOT EXISTS pg_temp.ta_space_sentinel"
	}
	for _, body := range []string{"SELECT 1/0 AS poison", "SELECT nextval('ta_space_draw') AS poison"} {
		skip := skipPrefix + "(changed) ON COMMIT DROP AS " + body
		if noData {
			skip += " WITH NO DATA"
		}
		tempAsAdmissionNotice(s, notices, "ta_space_sentinel", skip)
		tempAsAdmissionSentinel(s)
	}
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsAdmissionSentinel(s)
	tempAsRun(s, s.C, fixture.Select("SELECT nextval('ta_space_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
	// Persistent destinations still use the default policy, never TEMP policy.
	tempAsExec(s, s.C, "SET temp_tablespaces=''", 0)
	for _, persistent := range []string{"CREATE TABLE", "CREATE UNLOGGED TABLE"} {
		tempAsAdmissionGlobal(s, s.C, tempAsAdmissionQuery(persistent, "public.ta_space_persistent", noData))
		tempAsAdmissionAbsent(s, "public.ta_space_persistent")
	}
}

func tempAsAdmissionHeld(s *fixture.TempReferenceSession, _ *[]fixture.Diagnostic, prefix, name string, noData bool) {
	tempAsAdmissionSettings(s)
	q := tempAsAdmissionQuery(prefix, name, noData)
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	defer stmt.Close()
	tempAsAdmissionAbsent(s, name)
	tx, err := s.C.BeginTx(s.P.Ctx, nil)
	if err != nil {
		s.T.Fatal(err)
	}
	defer tx.Rollback()
	tempAsExec(s, tx, "SET LOCAL default_tablespace='pg_global'", 0)
	tempAsExec(s, tx, "SET LOCAL temp_tablespaces='pg_global'", 0)
	bound := tx.StmtContext(s.P.Ctx, stmt)
	defer bound.Close()
	d := fixture.ControlsReferenceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace")
	step := fixture.ErrorDiagnostic(q, d)
	step.ErrorStage = "execute"
	fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: tx, Statement: bound, SQL: q}, s.Record, step)
	if err := tx.Rollback(); err != nil {
		s.T.Fatal(err)
	}
	tempAsAdmissionAbsent(s, name)
	tempAsAdmissionPolicy(s, "")
	fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}, s.Record, fixture.Exec(q, tempAsAdmissionCount(noData)))
	tempAsAdmissionRows(s, name, noData)
}

func TestReferenceCreateTempTableAsTablespaces(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, destination := range []struct{ prefix, name, label string }{{"CREATE TEMP TABLE", "ta_space_ok", "TEMP"}, {"CREATE TABLE", "pg_temp.ta_space_alias", "pg_temp"}} {
			for _, policy := range []struct {
				label string
				body  func(*fixture.TempReferenceSession, *[]fixture.Diagnostic, string, string, bool)
			}{{"default-global", tempAsAdmissionDefault}, {"temp-global", tempAsAdmissionTempGlobal}, {"held", tempAsAdmissionHeld}} {
				t.Run(fmt.Sprintf("%s/no-data-%t/%s", destination.label, noData, policy.label), func(t *testing.T) {
					tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, notices *[]fixture.Diagnostic) {
						policy.body(s, notices, destination.prefix, destination.name, noData)
					})
				})
			}
		}
	}
}
