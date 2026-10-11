package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceJoinsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE a(l TEXT,k INT,q INT)", 0), fixture.Exec("CREATE TABLE b(r TEXT,q INT,k INT)", 0), fixture.Exec("CREATE TABLE c(k INT,c TEXT)", 0), fixture.Exec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0), fixture.Exec("CREATE TABLE wide(k BIGINT)", 0),
				fixture.Exec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4), fixture.Exec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4), fixture.Exec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2), fixture.Exec("INSERT INTO wide VALUES(1)", 1),
			} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
			body(t, p, conn, backend)
		})
	}
}

func TestReferenceQueryJoinsPreservation(t *testing.T) {
	referenceJoinsScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, backend fixture.Backend) {
		for _, tc := range []struct {
			query      string
			diagnostic fixture.Diagnostic
		}{
			{"SELECT k FROM a CROSS JOIN b", fixture.ControlsReferenceDiagnostic("42702", `column reference "k" is ambiguous`)},
			{"SELECT a.k FROM a AS x CROSS JOIN b", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Hint: `Perhaps you meant to reference the table alias "x".`}},
			{"SELECT a.k FROM (a JOIN b USING(k)) j", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`}},
		} {
			tc.diagnostic.Position = "8"
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(tc.query, tc.diagnostic)
				step.SelectRows, step.Prepared = true, prepared
				step.ErrorStage = "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE dst(k INT)", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO dst SELECT k FROM a JOIN b USING(k)", 4))
		fixture.OuterJoinsCases(t, p, conn, backend.Recorder, fixture.Select("SELECT k FROM dst", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
		// Native controls are explicit; SQL observations above run on both backends.
		if !backend.PG {
			for _, replacement := range []string{"reset", "restore"} {
				direct, err := conn.QueryContext(p.Ctx, "SELECT dst.k,wide.k FROM dst JOIN wide USING(k)")
				if err != nil {
					t.Fatal(err)
				}
				defer direct.Close()
				computed, err := conn.QueryContext(p.Ctx, "SELECT dst.k+1 FROM dst JOIN wide USING(k)")
				if err != nil {
					t.Fatal(err)
				}
				defer computed.Close()
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE dst(k INT);INSERT INTO dst VALUES(9);CREATE TABLE wide(k BIGINT);INSERT INTO wide VALUES(9)"))
				}
				if err != nil {
					t.Fatal(err)
				}
				fixture.OuterJoinsHeldRows(t, direct, fixture.Select("", []string{"k", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}}))
				fixture.OuterJoinsHeldRows(t, computed, fixture.Select("", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}, {int64(2)}}))
				if replacement == "reset" {
					for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE dst(k INT)", 0), fixture.Exec("INSERT INTO dst VALUES(1),(1),(1),(1)", 4), fixture.Exec("CREATE TABLE wide(k BIGINT)", 0), fixture.Exec("INSERT INTO wide VALUES(1)", 1)} {
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
					}
				} else {
					fixture.OuterJoinsCases(t, p, conn, backend.Recorder, fixture.Select("SELECT k FROM dst JOIN wide USING(k)", []string{"k"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
			}
		}
	})
}
