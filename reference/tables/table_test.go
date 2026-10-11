package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableCoreOrderedLikeShapeAndDrop(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec("CREATE TABLE source(i INTEGER NOT NULL,s TEXT,f BOOLEAN)", 0))
			run(fixture.Exec("INSERT INTO source VALUES($1,$2,$3)", 1, 7, "O'Brien", true))
			run(fixture.Exec("CREATE TABLE clone(lead BIGINT,LIKE source,tail TEXT)", 0))
			names := []string{"lead", "i", "s", "f", "tail"}
			types := []string{"INT8", "INT4", "TEXT", "BOOL", "TEXT"}
			run(fixture.Select("SELECT * FROM clone", names, types, nil))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM clone", names, types, []bool{true, false, true, true, true}, 0)
			}
			run(fixture.Exec("INSERT INTO clone VALUES($1,$2,$3,$4,$5)", 1, int64(9223372036854775807), 7, "O'Brien", true, "tail"))
			expected := [][]any{{int64(9223372036854775807), int64(7), "O'Brien", true, "tail"}}
			run(fixture.Select("SELECT * FROM clone", names, types, expected))
			failure := fixture.ImplicitError("INSERT INTO clone(i) VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "clone" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null, null).", Schema: "public", Table: "clone", Column: "i"})
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Select("SELECT * FROM clone", names, types, expected))
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"CREATE TABLE missingclone (LIKE missing)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "33"}},
				{"CREATE TABLE bad(LIKE source,i TEXT)", fixture.Diagnostic{Code: "42701", Message: `column "i" specified more than once`}},
				{"CREATE TABLE bad(ctid INT)", fixture.Diagnostic{Code: "42701", Message: `column name "ctid" conflicts with a system column name`}},
				{"DROP TABLE source,missing,clone", fixture.Diagnostic{Code: "42P01", Message: `table "missing" does not exist`}},
			} {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Select("SELECT * FROM source", []string{"i", "s", "f"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "O'Brien", true}}))
			run(fixture.Select("SELECT * FROM clone", names, types, expected))
			run(fixture.Exec("CREATE TABLE one(a INT4 NOT NULL)", 0))
			run(fixture.Exec("CREATE TABLE two(b TEXT)", 0))
			run(fixture.Exec("CREATE TABLE interleaved(head BOOL,LIKE one,middle INT8,LIKE two,tail TEXT)", 0))
			run(fixture.Select("SELECT * FROM interleaved", []string{"head", "a", "middle", "b", "tail"}, []string{"BOOL", "INT4", "INT8", "TEXT", "TEXT"}, nil))
			for _, b := range p.Backends() {
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM interleaved", []string{"head", "a", "middle", "b", "tail"}, []string{"BOOL", "INT4", "INT8", "TEXT", "TEXT"}, []bool{true, false, true, true, true}, 0)
			}
			for _, q := range []string{"CREATE TABLE bad(LIKE one,a TEXT)", "CREATE TABLE bad(LIKE one,LIKE one)"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("DROP TABLE source RESTRICT", 0))
			run(fixture.Select("SELECT * FROM clone", names, types, expected))
			run(fixture.Exec("INSERT INTO clone VALUES(9,8,NULL,NULL,NULL)", 1))
			run(fixture.Select("SELECT * FROM clone ORDER BY i", names, types, [][]any{expected[0], {int64(9), int64(8), nil, nil, nil}}))
			run(fixture.Exec("CREATE TABLE z()", 0))
			run(fixture.Exec("CREATE TABLE zl(LIKE z)", 0))
			for _, n := range []string{"z", "zl"} {
				run(fixture.Select("SELECT * FROM "+n, []string{}, []string{}, nil))
				for _, b := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM "+n, []string{}, []string{}, []bool{}, 0)
				}
			}
			for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
				run(fixture.Exec("CREATE TABLE a(id INT)", 0))
				run(fixture.Exec("CREATE TABLE b(id INT)", 0))
				run(fixture.Exec("INSERT INTO a VALUES(11)", 1))
				run(fixture.Exec("INSERT INTO b VALUES(22)", 1))
				failure := fixture.ImplicitError("DROP TABLE a,missing,b"+mode, fixture.Diagnostic{Code: "42P01", Message: `table "missing" does not exist`})
				failure.ErrorStage = "execute"
				run(failure)
				run(fixture.Select("SELECT * FROM a", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				run(fixture.Select("SELECT * FROM b", []string{"id"}, []string{"INT4"}, [][]any{{int64(22)}}))
				run(fixture.Exec("DROP TABLE IF EXISTS a,missing,public.a,b"+mode, 0))
				// Lookup failure is prepare-time on both engines for a newly prepared SELECT.
				for _, n := range []string{"a", "b"} {
					s := fixture.ImplicitError("SELECT * FROM "+n, fixture.Diagnostic{Code: "42P01", Message: "relation \"" + n + "\" does not exist", Position: "15"})
					s.SelectRows = true
					if prepared {
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
			}
			run(fixture.Exec(`CREATE TABLE "odd.dot"("Case" INT)`, 0))
			run(fixture.Exec(`DROP TABLE public."odd.dot",z,zl CASCADE`, 0))
			run(fixture.Exec("DROP TABLE clone,public.clone CASCADE", 0))
		})
	}
}
func TestReferenceTableCoreSemanticSkipNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				postgres := backend.PG
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, record, s) }
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(11)", 1))
					for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(other TEXT)", "CREATE TABLE IF NOT EXISTS t(id no_such_type)", `CREATE TABLE IF NOT EXISTS t(id "integer")`, "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(LIKE missing INCLUDING ALL)", "CREATE TABLE IF NOT EXISTS t(LIKE missing EXCLUDING CONSTRAINTS)"} {
						notices = nil
						run(fixture.Exec(q, 0))
						want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
						if !reflect.DeepEqual(notices, want) {
							t.Fatal(notices, want)
						}
						run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					notices = nil
					malformedQuery := "CREATE TABLE IF NOT EXISTS t(id no_such_type,)"
					malformed := fixture.ImplicitError(malformedQuery, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: fixture.Position(len(malformedQuery))})
					if prepared {
						malformed.ErrorStage = "prepare"
					} else {
						malformed.ErrorStage = "execute"
					}
					run(malformed)
					if len(notices) != 0 {
						t.Fatal("malformed skip emitted notice", notices)
					}
					run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					notices = nil
					run(fixture.Exec("DROP TABLE IF EXISTS missing,absent_schema.t", 0))
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "absent_schema" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices, want)
					}
					notices = nil
					run(fixture.Exec("CREATE TABLE a(id INT)", 0))
					run(fixture.Exec("CREATE TABLE b(id INT)", 0))
					notices = nil
					fixture.RunStep(t, p.Ctx, conn, record, fixture.Prepared(fixture.Exec("DROP TABLE IF EXISTS a,missing,public.a,b CASCADE", 0)))
					groupedNotice := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, groupedNotice) {
						t.Fatal("grouped DROP notice", notices, groupedNotice)
					}
					for _, name := range []string{"a", "b"} {
						absent := fixture.ImplicitError("SELECT * FROM "+name, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
						absent.SelectRows = true
						if prepared {
							absent.ErrorStage = "prepare"
						} else {
							absent.ErrorStage = "execute"
						}
						run(absent)
					}
					notices = nil
					run(fixture.Exec("SET client_min_messages TO warning", 0))
					run(fixture.Exec("CREATE TABLE IF NOT EXISTS t(id no_such_type)", 0))
					run(fixture.Exec("DROP TABLE IF EXISTS missing", 0))
					if len(notices) != 0 {
						t.Fatal(notices)
					}
					fixture.RunStep(t, p.Ctx, conn, record, fixture.Prepared(fixture.Exec("CREATE TABLE IF NOT EXISTS t(id unknown_type)", 0)))
					if len(notices) != 0 {
						t.Fatal("prepared suppressed skip emitted notice", notices)
					}
					run(fixture.Exec("RESET client_min_messages", 0))
					notices = nil
					fixture.RunStep(t, p.Ctx, conn, record, fixture.Prepared(fixture.Exec("DROP TABLE IF EXISTS missing,absent_schema.t", 0)))
					if !reflect.DeepEqual(notices, want) {
						t.Fatal("reset did not restore ordered notices", notices, want)
					}
					run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
				})
			}
		})
	}
}
