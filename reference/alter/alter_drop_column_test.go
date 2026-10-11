package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDropColumnShape(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, insertPrepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("ddl=%v/insert=%v", prepared, insertPrepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
				run(fixture.Exec("CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)", 2))
				run(fixture.Exec("ALTER TABLE t DROP COLUMN b RESTRICT", 0))
				for _, backend := range p.Backends() {
					for _, mode := range []bool{false, true} {
						fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, mode, "SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, []bool{false, true}, 2)
					}
				}
				run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}}))
				run(fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
				insert := fixture.Exec("INSERT INTO t(c,a) VALUES($1,$2)", 1, 40, 4)
				insert.Prepared = insertPrepared
				p.Step(t, insert)
				run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
				for _, tc := range []struct{ q, code, message string }{{"ALTER TABLE t DROP missing", "42703", `column "missing" of relation "t" does not exist`}, {"ALTER TABLE t DROP IF EXISTS ctid", "0A000", `cannot drop system column "ctid"`}} {
					failure := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					failure.ErrorStage = "execute"
					run(failure)
				}
				run(fixture.Exec("ALTER TABLE ONLY(t) DROP c CASCADE", 0))
				for _, backend := range p.Backends() {
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, true, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 4)
				}
				for _, form := range []string{"t", "ONLY t", "ONLY(t)", "t*"} {
					run(fixture.Exec("DROP TABLE t", 0))
					run(fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,'gone')", 1))
					run(fixture.Exec("ALTER TABLE "+form+" DROP b CASCADE", 0))
					run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		}
	}
}

func TestReferenceAlterDropColumnNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"drop_reader"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, record, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
					notices = nil
					run(fixture.Exec("ALTER TABLE t DROP COLUMN IF EXISTS missing", 0))
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("skip notices got %#v want %#v", notices, want)
					}
					run(fixture.Exec("SET client_min_messages TO warning", 0))
					notices = nil
					run(fixture.Exec("ALTER TABLE t DROP IF EXISTS missing", 0))
					if len(notices) != 0 {
						t.Fatalf("suppressed notices: %#v", notices)
					}
					run(fixture.Exec("RESET client_min_messages", 0))
					run(fixture.Exec("CREATE USER drop_reader", 0))
					if backend.PG {
						// Match native public-schema visibility before testing table ownership.
						fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("GRANT USAGE ON SCHEMA public TO drop_reader", 0))
					}
					defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					run(fixture.Exec("SET SESSION AUTHORIZATION drop_reader", 0))
					fixture.RunStep(t, p.Ctx, conn, record, fixture.RoleReferenceError("ALTER TABLE t DROP IF EXISTS missing", "42501", "must be owner of table t", "", true, "execute"))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				})
			}
		})
	}
}

func TestReferenceAlterDropColumnPreparedLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, record := backend.DB, backend.Recorder
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, db, record, s) }
					run(fixture.Exec("CREATE TABLE held(a INT NOT NULL,b TEXT,c BIGINT)", 0))
					run(fixture.Exec("INSERT INTO held VALUES(1,'drop',9),(2,NULL,NULL)", 2))
					survivor, err := db.PrepareContext(p.Ctx, "SELECT a,c FROM held ORDER BY a")
					if err != nil {
						t.Fatal(err)
					}
					defer survivor.Close()
					star, err := db.PrepareContext(p.Ctx, "SELECT * FROM held")
					if err != nil {
						t.Fatal(err)
					}
					defer star.Close()
					dropped, err := db.PrepareContext(p.Ctx, "SELECT b FROM held")
					if err != nil {
						t.Fatal(err)
					}
					defer dropped.Close()
					// Native keeps the actual eager cursor alive across mutation; pq's portal is closed before AX.
					cursorDB := db
					if !backend.PG {
						cursorDB = p.Native
					}
					held, err := cursorDB.QueryContext(p.Ctx, "SELECT * FROM held ORDER BY a")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					consume := func() {
						labels, e := held.Columns()
						if e != nil || !reflect.DeepEqual(labels, []string{"a", "b", "c"}) {
							t.Fatal(labels, e)
						}
						if !held.Next() {
							t.Fatal("missing original row", held.Err())
						}
						var a, c int64
						var b string
						if e = held.Scan(&a, &b, &c); e != nil || a != 1 || b != "drop" || c != 9 {
							t.Fatal(a, b, c, e)
						}
						if e = held.Close(); e != nil {
							t.Fatal(e)
						}
					}
					if backend.PG {
						consume()
					}
					mutation := fixture.Exec("ALTER TABLE held DROP b", 0)
					mutation.Prepared = prepared
					run(mutation)
					if !backend.PG {
						consume()
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{db, survivor, "SELECT a,c FROM held ORDER BY a"}, record, fixture.Select("SELECT a,c FROM held ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}}))
					for _, tc := range []struct{ q, code, message, position string }{{"SELECT * FROM held", "0A000", "cached plan must not change result type", ""}, {"SELECT b FROM held", "42703", `column "b" does not exist`, "8"}} {
						stmt := star
						if tc.position != "" {
							stmt = dropped
						}
						failure := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
						failure.SelectRows = true
						failure.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{db, stmt, tc.q}, record, failure)
					}
					failure := fixture.ImplicitError("SELECT b FROM held", fixture.Diagnostic{Code: "42703", Message: `column "b" does not exist`, Position: "8"})
					failure.SelectRows = true
					failure.Prepared = true
					failure.ErrorStage = "prepare"
					run(failure)
				})
			}
		})
	}
}

func TestReferenceAlterDropColumnTransactionalDDL(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
	for _, backend := range p.Backends() {
		func() {
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("ALTER TABLE t DROP b", 0))
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
}
