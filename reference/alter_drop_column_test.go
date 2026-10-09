package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceAlterDropColumnShape(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, insertPrepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("ddl=%v/insert=%v", prepared, insertPrepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
				run(referenceExec("CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)", 2))
				run(referenceExec("ALTER TABLE t DROP COLUMN b RESTRICT", 0))
				for _, backend := range p.backends() {
					for _, mode := range []bool{false, true} {
						referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, mode, "SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, []bool{false, true}, 2)
					}
				}
				run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}}))
				run(referenceExec("INSERT INTO t VALUES(3,30)", 1))
				insert := referenceExec("INSERT INTO t(c,a) VALUES($1,$2)", 1, 40, 4)
				insert.prepared = insertPrepared
				p.step(t, insert)
				run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
				for _, tc := range []struct{ q, code, message string }{{"ALTER TABLE t DROP missing", "42703", `column "missing" of relation "t" does not exist`}, {"ALTER TABLE t DROP IF EXISTS ctid", "0A000", `cannot drop system column "ctid"`}} {
					failure := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message})
					failure.errorStage = "execute"
					run(failure)
				}
				run(referenceExec("ALTER TABLE ONLY(t) DROP c CASCADE", 0))
				for _, backend := range p.backends() {
					referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, true, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 4)
				}
				for _, form := range []string{"t", "ONLY t", "ONLY(t)", "t*"} {
					run(referenceExec("DROP TABLE t", 0))
					run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,'gone')", 1))
					run(referenceExec("ALTER TABLE "+form+" DROP b CASCADE", 0))
					run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		}
	}
}

func TestReferenceAlterDropColumnNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"drop_reader"})
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,'keep')", 1))
					notices = nil
					run(referenceExec("ALTER TABLE t DROP COLUMN IF EXISTS missing", 0))
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("skip notices got %#v want %#v", notices, want)
					}
					run(referenceExec("SET client_min_messages TO warning", 0))
					notices = nil
					run(referenceExec("ALTER TABLE t DROP IF EXISTS missing", 0))
					if len(notices) != 0 {
						t.Fatalf("suppressed notices: %#v", notices)
					}
					run(referenceExec("RESET client_min_messages", 0))
					run(referenceExec("CREATE USER drop_reader", 0))
					if backend.pg {
						// Match native public-schema visibility before testing table ownership.
						referenceRunStep(t, p.ctx, conn, record, referenceExec("GRANT USAGE ON SCHEMA public TO drop_reader", 0))
					}
					defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					run(referenceExec("SET SESSION AUTHORIZATION drop_reader", 0))
					referenceRunStep(t, p.ctx, conn, record, roleReferenceError("ALTER TABLE t DROP IF EXISTS missing", "42501", "must be owner of table t", "", true, "execute"))
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				})
			}
		})
	}
}

func TestReferenceAlterDropColumnPreparedLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db, record := backend.db, backend.recorder
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
					run(referenceExec("CREATE TABLE held(a INT NOT NULL,b TEXT,c BIGINT)", 0))
					run(referenceExec("INSERT INTO held VALUES(1,'drop',9),(2,NULL,NULL)", 2))
					survivor, err := db.PrepareContext(p.ctx, "SELECT a,c FROM held ORDER BY a")
					if err != nil {
						t.Fatal(err)
					}
					defer survivor.Close()
					star, err := db.PrepareContext(p.ctx, "SELECT * FROM held")
					if err != nil {
						t.Fatal(err)
					}
					defer star.Close()
					dropped, err := db.PrepareContext(p.ctx, "SELECT b FROM held")
					if err != nil {
						t.Fatal(err)
					}
					defer dropped.Close()
					// Native keeps the actual eager cursor alive across mutation; pq's portal is closed before AX.
					cursorDB := db
					if !backend.pg {
						cursorDB = p.native
					}
					held, err := cursorDB.QueryContext(p.ctx, "SELECT * FROM held ORDER BY a")
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
					if backend.pg {
						consume()
					}
					mutation := referenceExec("ALTER TABLE held DROP b", 0)
					mutation.prepared = prepared
					run(mutation)
					if !backend.pg {
						consume()
					}
					referenceRunStep(t, p.ctx, referenceHeldStatement{db, survivor, "SELECT a,c FROM held ORDER BY a"}, record, referenceSelect("SELECT a,c FROM held ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}, {int64(2), nil}}))
					for _, tc := range []struct{ q, code, message, position string }{{"SELECT * FROM held", "0A000", "cached plan must not change result type", ""}, {"SELECT b FROM held", "42703", `column "b" does not exist`, "8"}} {
						stmt := star
						if tc.position != "" {
							stmt = dropped
						}
						failure := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
						failure.selectRows = true
						failure.errorStage = "execute"
						referenceRunStep(t, p.ctx, referenceHeldStatement{db, stmt, tc.q}, record, failure)
					}
					failure := referenceImplicitError("SELECT b FROM held", referenceDiagnostic{Code: "42703", Message: `column "b" does not exist`, Position: "8"})
					failure.selectRows = true
					failure.prepared = true
					failure.errorStage = "prepare"
					run(failure)
				})
			}
		})
	}
}

func TestReferenceAlterDropColumnTransactionalDDL(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'keep')", 1))
	for _, backend := range p.backends() {
		func() {
			tx, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("ALTER TABLE t DROP b", 0))
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
}
