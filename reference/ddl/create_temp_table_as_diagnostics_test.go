package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCreateTempTableAsDiagnostics(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_names(renamed) AS SELECT 1 AS x,'a'::text AS y", 1)
		tempAsRun(s, s.C, fixture.Select("SELECT * FROM ta_names", []string{"renamed", "y"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		tempAsExec(s, s.C, `CREATE TEMPORARY TABLE ta_unicode("名", "Mixed") AS SELECT 1 AS x,'a'::text AS y`, 1)
		tempAsRun(s, s.C, fixture.Select("SELECT * FROM ta_unicode", []string{"名", "Mixed"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		for _, c := range []struct {
			q, code, message, pos string
			analysis              bool
		}{
			{"CREATE TEMP TABLE ta_bad(a,b) AS SELECT 1 AS x", "42601", "too many column names were specified", "", false},
			{"CREATE TEMP TABLE ta_bad AS SELECT 1 AS x,2 AS x", "42701", `column "x" specified more than once`, "", false},
			{"CREATE TEMP TABLE ta_bad AS SELECT 1 AS ctid", "42701", `column name "ctid" conflicts with a system column name`, "", false},
			{"CREATE TEMP TABLE absent_schema.ta_bad AS SELECT 1 AS v", "3F000", `schema "absent_schema" does not exist`, "", false},
			{"CREATE TEMP TABLE public.ta_bad AS SELECT 1 AS v", "42P16", "cannot create temporary relation in non-temporary schema", "", false},
			{"CREATE TEMP TABLE ta_bad AS SELECT * FROM ta_missing WITH NO DATA", "42P01", `relation "ta_missing" does not exist`, "43", true},
			{"CREATE TEMP TABLE ta_bad AS SELECT unknown FROM ta_names WITH NO DATA", "42703", `column "unknown" does not exist`, "36", true},
		} {
			tempAsError(s, s.C, c.q, c.code, c.message, c.pos, c.analysis)
			tempAsAbsent(s, "ta_bad")
		}
		for _, prefix := range []string{"CREATE TABLE", "CREATE UNLOGGED TABLE"} {
			for _, action := range []string{"PRESERVE ROWS", "DELETE ROWS", "DROP"} {
				tempAsError(s, s.C, prefix+" ta_bad ON COMMIT "+action+" AS SELECT 1/0 AS v", "22012", "division by zero", "", false)
				tempAsAbsent(s, "ta_bad")
				tempAsError(s, s.C, prefix+" ta_bad ON COMMIT "+action+" AS SELECT 1 AS v", "42P16", "ON COMMIT can only be used on temporary tables", "", false)
				tempAsAbsent(s, "ta_bad")
				tempAsError(s, s.C, prefix+" ta_bad ON COMMIT "+action+" AS SELECT 1/(renamed-renamed) AS v FROM ta_names", "42P16", "ON COMMIT can only be used on temporary tables", "", false)
				tempAsAbsent(s, "ta_bad")
				tempAsError(s, s.C, prefix+" ta_bad ON COMMIT "+action+" AS SELECT 1/0 AS v WITH NO DATA", "42P16", "ON COMMIT can only be used on temporary tables", "", false)
				tempAsAbsent(s, "ta_bad")
			}
		}
		for _, c := range []struct{ q, token string }{
			{"CREATE TEMP TABLE ta_bad ON COMMIT DROP ON COMMIT DROP AS SELECT 1 AS v", "ON COMMIT DROP AS"},
			{"CREATE TEMP TABLE ta_bad ON COMMIT PRESERVE GROUPS AS SELECT 1 AS v", "GROUPS"},
			{"CREATE TEMP TABLE ta_bad AS SELECT 1 AS v ON COMMIT DROP", "ON"},
			{"CREATE TEMP TABLE ta_bad AS SELECT 1 AS v WITH NO BANANAS", "BANANAS"},
			{"CREATE TEMP TABLE ta_bad ON COMMIT DROP AS SELECT 1 AS v garbage", "garbage"},
		} {
			token := strings.Fields(c.token)[0]
			tempAsError(s, s.C, c.q, "42601", `syntax error at or near "`+token+`"`, fixture.Position(strings.Index(c.q, c.token)+1), true)
			tempAsAbsent(s, "ta_bad")
		}
		tempAsExec(s, s.C, "CREATE TEMP TABLE ta_duplicate AS SELECT 7 AS v", 1)
		tempAsError(s, s.C, "CREATE TEMP TABLE ta_duplicate AS SELECT 1/0 AS v", "42P07", `relation "ta_duplicate" already exists`, "", false)
		tempAsRows(s, s.C, "ta_duplicate", 7)
	})
}
func TestReferenceCreateTempTableAsIfNotExists(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					peer, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					s := &fixture.TempReferenceSession{T: t, P: p, DB: db, C: conn, Peer: peer, Record: record, Prepared: prepared, Postgres: b.PG}
					tempAsExec(s, s.C, "CREATE TEMP TABLE ta_existing ON COMMIT PRESERVE ROWS AS SELECT 'sentinel'::text AS v", 1)
					tempAsExec(s, s.C, "CREATE SEQUENCE ta_skip_draw START 10", 0)
					for _, body := range []string{"SELECT 1/0 AS x", "SELECT 'bad'::text::integer AS x", "SELECT nextval('ta_skip_draw') AS n"} {
						q := "CREATE TEMP TABLE IF NOT EXISTS ta_existing(other) ON COMMIT DROP AS " + body
						if prepared {
							stmt, err := conn.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							if len(notices) != 0 {
								t.Fatal("Prepare notice", notices)
							}
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, record, fixture.Exec(q, 0))
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							tempAsExec(s, conn, q, 0)
						}
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "ta_existing" already exists, skipping`}
						if len(notices) != 1 || notices[0] != want {
							t.Fatalf("notices=%#v want=%#v", notices, want)
						}
						notices = nil
						tempAsRun(s, conn, fixture.Select("SELECT * FROM ta_existing", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
					}
					tempAsExec(s, conn, "BEGIN", 0)
					tempAsExec(s, conn, "COMMIT", 0)
					tempAsRun(s, conn, fixture.Select("SELECT v FROM ta_existing", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
					tempAsRun(s, conn, fixture.Select("SELECT nextval('ta_skip_draw') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
					for _, body := range []struct{ sql, code, message, token string }{{"SELECT * FROM ta_missing", "42P01", `relation "ta_missing" does not exist`, "ta_missing"}, {"SELECT unknown FROM ta_existing", "42703", `column "unknown" does not exist`, "unknown"}} {
						q := "CREATE TEMP TABLE IF NOT EXISTS ta_existing AS " + body.sql
						tempAsError(s, conn, q, body.code, body.message, fixture.Position(strings.Index(q, body.token)+1), true)
					}
					tempAsExec(s, conn, "BEGIN", 0)
					tempAsExec(s, conn, "CREATE TEMP TABLE ta_skip_delete ON COMMIT DELETE ROWS AS SELECT 1 AS v", 1)
					tempAsExec(s, conn, "CREATE TEMP TABLE IF NOT EXISTS ta_skip_delete ON COMMIT PRESERVE ROWS AS SELECT 1/0 AS x", 0)
					tempAsRows(s, conn, "ta_skip_delete", 1)
					tempAsExec(s, conn, "COMMIT", 0)
					tempAsRows(s, conn, "ta_skip_delete")
					wantDelete := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "ta_skip_delete" already exists, skipping`}
					if len(notices) != 1 || notices[0] != wantDelete {
						t.Fatal(notices)
					}
					notices = nil
					for _, prefix := range []string{"CREATE TABLE", "CREATE UNLOGGED TABLE"} {
						tempAsExec(s, conn, prefix+" ta_skip_regular AS SELECT 7 AS v", 1)
						tempAsExec(s, conn, prefix+" IF NOT EXISTS ta_skip_regular ON COMMIT DROP AS SELECT 1/0 AS v", 0)
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "ta_skip_regular" already exists, skipping`}
						if len(notices) != 1 || notices[0] != want {
							t.Fatal(notices)
						}
						notices = nil
						tempAsRows(s, conn, "ta_skip_regular", 7)
						tempAsExec(s, conn, "DROP TABLE ta_skip_regular", 0)
					}
				})
			}
		})
	}
}
func TestReferenceCreateTempTableAsUnlogged(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "CREATE UNLOGGED TABLE ta_unlogged AS SELECT v FROM (VALUES(1),(2)) q(v) WITH DATA", 2)
		tempAsExec(s, s.C, "COMMIT", 0)
		tempAsRows(s, s.Peer, "ta_unlogged", 1, 2)
		tempAsRun(s, s.C, fixture.Select("SELECT relpersistence::text AS p FROM pg_class WHERE oid='ta_unlogged'::regclass", []string{"p"}, []string{"TEXT"}, [][]any{{"u"}}))
		tempAsExec(s, s.C, "CREATE UNLOGGED TABLE ta_unlogged_empty AS SELECT 1/0 AS v WITH NO DATA", 0)
		tempAsRows(s, s.C, "ta_unlogged_empty")
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "CREATE UNLOGGED TABLE ta_unlogged_undo AS SELECT 1 AS v", 1)
		tempAsExec(s, s.C, "ROLLBACK", 0)
		tempAsAbsent(s, "ta_unlogged_undo")
		var namespace uint64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT pg_my_temp_schema()").Scan(&namespace); err != nil || namespace != 0 {
			s.T.Fatal("UNLOGGED allocated TEMP namespace", namespace, err)
		}
	})
}
