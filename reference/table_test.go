//go:build reference

package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceTableCoreOrderedLikeShapeAndDrop(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec("CREATE TABLE source(i INTEGER NOT NULL,s TEXT,f BOOLEAN)", 0))
			run(referenceExec("INSERT INTO source VALUES($1,$2,$3)", 1, 7, "O'Brien", true))
			run(referenceExec("CREATE TABLE clone(lead BIGINT,LIKE source,tail TEXT)", 0))
			names := []string{"lead", "i", "s", "f", "tail"}
			types := []string{"INT8", "INT4", "TEXT", "BOOL", "TEXT"}
			run(referenceSelect("SELECT * FROM clone", names, types, nil))
			run(referenceExec("INSERT INTO clone VALUES($1,$2,$3,$4,$5)", 1, int64(9223372036854775807), 7, "O'Brien", true, "tail"))
			expected := [][]any{{int64(9223372036854775807), int64(7), "O'Brien", true, "tail"}}
			run(referenceSelect("SELECT * FROM clone", names, types, expected))
			failure := referenceImplicitError("INSERT INTO clone(i) VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null, null).", Schema: "public", Table: "clone", Column: "i"})
			failure.errorStage = "execute"
			run(failure)
			run(referenceSelect("SELECT * FROM clone", names, types, expected))
			run(referenceExec("CREATE TABLE one(a INT4 NOT NULL)", 0))
			run(referenceExec("CREATE TABLE two(b TEXT)", 0))
			run(referenceExec("CREATE TABLE interleaved(head BOOL,LIKE one,middle INT8,LIKE two,tail TEXT)", 0))
			run(referenceSelect("SELECT * FROM interleaved", []string{"head", "a", "middle", "b", "tail"}, []string{"BOOL", "INT4", "INT8", "TEXT", "TEXT"}, nil))
			for _, q := range []string{"CREATE TABLE bad(LIKE one,a TEXT)", "CREATE TABLE bad(LIKE one,LIKE one)"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42701", Message: `column "a" specified more than once`})
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("DROP TABLE source RESTRICT", 0))
			run(referenceSelect("SELECT * FROM clone", names, types, expected))
			run(referenceExec("INSERT INTO clone VALUES(9,8,NULL,NULL,NULL)", 1))
			run(referenceSelect("SELECT * FROM clone ORDER BY i", names, types, [][]any{expected[0], {int64(9), int64(8), nil, nil, nil}}))
			run(referenceExec("CREATE TABLE z()", 0))
			run(referenceExec("CREATE TABLE zl(LIKE z)", 0))
			for _, n := range []string{"z", "zl"} {
				run(referenceSelect("SELECT * FROM "+n, []string{}, []string{}, nil))
			}
			for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
				run(referenceExec("CREATE TABLE a(id INT)", 0))
				run(referenceExec("CREATE TABLE b(id INT)", 0))
				run(referenceExec("INSERT INTO a VALUES(11)", 1))
				run(referenceExec("INSERT INTO b VALUES(22)", 1))
				failure := referenceImplicitError("DROP TABLE a,missing,b"+mode, referenceDiagnostic{Code: "42P01", Message: `table "missing" does not exist`})
				failure.errorStage = "execute"
				run(failure)
				run(referenceSelect("SELECT * FROM a", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				run(referenceSelect("SELECT * FROM b", []string{"id"}, []string{"INT4"}, [][]any{{int64(22)}}))
				run(referenceExec("DROP TABLE IF EXISTS a,missing,public.a,b"+mode, 0))
				// Lookup failure is prepare-time on both engines for a newly prepared SELECT.
				for _, n := range []string{"a", "b"} {
					s := referenceImplicitError("SELECT * FROM "+n, referenceDiagnostic{Code: "42P01", Message: "relation \"" + n + "\" does not exist"})
					s.selectRows = true
					pg := *s.diagnostic
					pg.Position = "15"
					s.postgresDiagnostic = &pg
					if prepared {
						s.errorStage = "prepare"
					} else {
						s.errorStage = "execute"
					}
					run(s)
				}
			}
			run(referenceExec(`CREATE TABLE "odd.dot"("Case" INT)`, 0))
			run(referenceExec(`DROP TABLE public."odd.dot",z,zl CASCADE`, 0))
		})
	}
}
func TestReferenceTableCoreSemanticSkipNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []referenceDiagnostic
					_, conn, record := noticeReferencePool(t, p, postgres, &notices)
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, record, s) }
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(11)", 1))
					for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(id no_such_type)", "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(LIKE missing INCLUDING ALL)"} {
						notices = nil
						run(referenceExec(q, 0))
						want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal(notices, want)
						}
						run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					notices = nil
					run(referenceExec("DROP TABLE IF EXISTS missing,absent_schema.t", 0))
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "absent_schema" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices, want)
					}
					notices = nil
					run(referenceExec("SET client_min_messages TO warning", 0))
					run(referenceExec("CREATE TABLE IF NOT EXISTS t(id no_such_type)", 0))
					run(referenceExec("DROP TABLE IF EXISTS missing", 0))
					if len(notices) != 0 {
						t.Fatal(notices)
					}
				})
			}
		})
	}
}
