package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func referenceJoinsScenario(t *testing.T, body func(*testing.T, *referencePair, *sql.Conn, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE a(l TEXT,k INT,q INT)", 0), referenceExec("CREATE TABLE b(r TEXT,q INT,k INT)", 0), referenceExec("CREATE TABLE c(k INT,c TEXT)", 0), referenceExec("CREATE TABLE empty(r TEXT,q INT,k INT)", 0), referenceExec("CREATE TABLE wide(k BIGINT)", 0),
				referenceExec("INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", 4), referenceExec("INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", 4), referenceExec("INSERT INTO c VALUES(1,'c'),(1,'c')", 2), referenceExec("INSERT INTO wide VALUES(1)", 1),
			} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, step)
			}
			body(t, p, conn, backend)
		})
	}
}

func TestReferenceQueryJoinsPreservation(t *testing.T) {
	referenceJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, backend referenceBackend) {
		for _, tc := range []struct {
			query      string
			diagnostic referenceDiagnostic
		}{
			{"SELECT k FROM a CROSS JOIN b", controlsReferenceDiagnostic("42702", `column reference "k" is ambiguous`)},
			{"SELECT a.k FROM a AS x CROSS JOIN b", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Hint: `Perhaps you meant to reference the table alias "x".`}},
			{"SELECT a.k FROM (a JOIN b USING(k)) j", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `invalid reference to FROM-clause entry for table "a"`, Detail: `There is an entry for table "a", but it cannot be referenced from this part of the query.`}},
		} {
			tc.diagnostic.Position = "8"
			for _, prepared := range []bool{false, true} {
				step := referenceErrorDiagnostic(tc.query, tc.diagnostic)
				step.selectRows, step.prepared = true, prepared
				step.errorStage = "execute"
				if prepared {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, conn, backend.recorder, step)
			}
		}
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TABLE dst(k INT)", 0))
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO dst SELECT k FROM a JOIN b USING(k)", 4))
		outerJoinsCases(t, p, conn, backend.recorder, referenceSelect("SELECT k FROM dst", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
		// Native controls are explicit; SQL observations above run on both backends.
		if !backend.pg {
			for _, replacement := range []string{"reset", "restore"} {
				direct, err := conn.QueryContext(p.ctx, "SELECT dst.k,wide.k FROM dst JOIN wide USING(k)")
				if err != nil {
					t.Fatal(err)
				}
				defer direct.Close()
				computed, err := conn.QueryContext(p.ctx, "SELECT dst.k+1 FROM dst JOIN wide USING(k)")
				if err != nil {
					t.Fatal(err)
				}
				defer computed.Close()
				if replacement == "reset" {
					err = stuntgres.Reset(p.ctx, p.native)
				} else {
					err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE dst(k INT);INSERT INTO dst VALUES(9);CREATE TABLE wide(k BIGINT);INSERT INTO wide VALUES(9)"))
				}
				if err != nil {
					t.Fatal(err)
				}
				outerJoinsHeldRows(t, direct, referenceSelect("", []string{"k", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}, {int64(1), int64(1)}}))
				outerJoinsHeldRows(t, computed, referenceSelect("", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}, {int64(2)}}))
				if replacement == "reset" {
					for _, step := range []referenceStep{referenceExec("CREATE TABLE dst(k INT)", 0), referenceExec("INSERT INTO dst VALUES(1),(1),(1),(1)", 4), referenceExec("CREATE TABLE wide(k BIGINT)", 0), referenceExec("INSERT INTO wide VALUES(1)", 1)} {
						referenceRunStep(t, p.ctx, conn, backend.recorder, step)
					}
				} else {
					outerJoinsCases(t, p, conn, backend.recorder, referenceSelect("SELECT k FROM dst JOIN wide USING(k)", []string{"k"}, []string{"INT8"}, [][]any{{int64(9)}}))
				}
			}
		}
	})
}

