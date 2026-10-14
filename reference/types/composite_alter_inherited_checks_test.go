package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterInheritedChecks(t *testing.T) {
	for _, graph := range []string{"child", "diamond"} {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(graph+"_"+name, func(t *testing.T) {
				fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
					var notices []fixture.Diagnostic
					_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, rec, s) }
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE root OF pair (CONSTRAINT keep_root CHECK(note<>'bad'))`)
					parents := "root"
					tables := []string{"root", "child"}
					if graph == "diamond" {
						fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE left_child() INHERITS(root)`, `CREATE TABLE right_child() INHERITS(root)`)
						parents = "left_child,right_child"
						tables = []string{"root", "left_child", "right_child", "child"}
					}
					fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE child(v pair,CONSTRAINT field_check CHECK((v).id>0),CONSTRAINT keep_local CHECK((v).note<>'blocked')) INHERITS("+parents+")")
					run(fixture.Exec(`INSERT INTO child(id,note,v) VALUES(7,'first',$1)`, 1, "(9,payload)"))
					provenance := func(observe fixture.Executor, backend fixture.Backend, dependent bool) {
						for _, table := range tables {
							count, local := int64(1), false
							if table == "root" {
								count, local = 0, true
							}
							if table == "child" && graph == "diamond" {
								count = 2
							}
							want := [][]any{{"keep_root", local, count}}
							if table == "child" {
								want = [][]any{{"keep_local", true, int64(0)}, {"keep_root", false, count}}
								if dependent {
									want = append([][]any{{"field_check", true, int64(0)}}, want...)
								}
							}
							s := fixture.Select("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid ORDER BY conname", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, want)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, observe, backend.Recorder, s)
						}
					}
					before := func() {
						provenance(c, b, true)
						for _, table := range tables {
							run(fixture.Select("SELECT id,note FROM "+table+" ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "first"}}))
						}
						fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM child`, [][]any{{"(9,payload)"}}, prepared)
					}
					before()
					// Typed-table RESTRICT takes precedence over field-CHECK discovery.
					d := fixture.CompositeReferenceDiagnostic("2BP01", `cannot alter type "pair" because it is the type of a typed table`)
					d.Hint = `Use ALTER ... CASCADE to alter the typed tables too.`
					notices = nil
					fixture.CompositeReferenceError(t, p, c, `ALTER TYPE pair DROP ATTRIBUTE id RESTRICT`, "execute", d)
					if len(notices) != 0 {
						t.Errorf("RESTRICT notices=%#v want none", notices)
					}
					before()
					notices = nil
					run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE id CASCADE`, 0))
					fixture.DomainReferenceNotice(t, &notices, `drop cascades to constraint field_check on table child`, "")
					provenance(c, b, false)
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM child`, [][]any{{"(payload)"}}, prepared)
					// Later propagation must use staged authority, not resurrect the CHECK.
					run(fixture.Exec(`ALTER TYPE pair ADD ATTRIBUTE extra integer CASCADE`, 0))
					provenance(c, b, false)
					run(fixture.Exec(`INSERT INTO child(note,v,extra) VALUES('second',$1,5)`, 1, "(accepted,)"))
					check := func(observe fixture.Executor, backend fixture.Backend) {
						provenance(observe, backend, false)
						for _, table := range tables {
							s := fixture.Select("SELECT note,extra FROM "+table+" ORDER BY note", []string{"note", "extra"}, []string{"TEXT", "INT4"}, [][]any{{"first", nil}, {"second", int64(5)}})
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, observe, backend.Recorder, s)
						}
						fixture.CompositeReferenceRows(t, p, backend, observe, `SELECT v FROM child ORDER BY note`, [][]any{{"(payload,)"}, {"(accepted,)"}}, prepared)
						fixture.RunStep(t, p.Ctx, observe, backend.Recorder, fixture.Select(`SELECT attname FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, [][]any{{"note"}, {"extra"}}))
						fixture.RunStep(t, p.Ctx, observe, backend.Recorder, fixture.Select(`SELECT reloftype='pair'::regtype::oid AS attached FROM pg_class WHERE oid='root'::regclass::oid`, []string{"attached"}, []string{"BOOL"}, [][]any{{true}}))
					}
					check(c, b)
					future := func(conn *sql.Conn, backend fixture.Backend, reconstructed bool) {
						names, types := []string{"note", "v", "extra"}, []string{"TEXT", "", "INT4"}
						values := [][]any{{"first", "(payload,)", nil}, {"second", "(accepted,)", int64(5)}}
						if reconstructed {
							names, types = []string{"note", "extra", "v"}, []string{"TEXT", "INT4", ""}
							values = [][]any{{"first", nil, "(payload,)"}, {"second", int64(5), "(accepted,)"}}
						}
						metadata := fixture.Select(`SELECT * FROM child ORDER BY note`, names, types, values)
						metadata.Prepared = prepared
						fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, metadata)
						for _, tc := range []struct{ note, value, constraint string }{{"bad", "(accepted,)", "keep_root"}, {"ok", "(blocked,)", "keep_local"}} {
							want := fixture.CompositeReferenceDiagnostic("23514", `new row for relation "child" violates check constraint "`+tc.constraint+`"`)
							want.Schema, want.Table, want.Constraint = "public", "child", tc.constraint
							want.Detail = "Failing row contains (" + tc.note + ", " + tc.value + ", 5)."
							if reconstructed {
								want.Detail = "Failing row contains (" + tc.note + ", 5, " + tc.value + ")."
							}
							fixture.CompositeReferenceError(t, p, conn, "INSERT INTO child(note,v,extra) VALUES('"+tc.note+"',$1,5)", "execute", want, tc.value)
						}
						s := fixture.Exec(`INSERT INTO child(note,v,extra) VALUES('third',$1,6)`, 1, "(free,)")
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(`DELETE FROM child WHERE note='third'`, 1))
					}
					future(c, b, false)
					check(c, b)
					if b.PG {
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					freshDriver := stuntgres.NewDriver()
					fresh := fixture.DriverPool(t, freshDriver, "composite-inherited-checks")
					connector, err := freshDriver.OpenConnector("composite-inherited-checks")
					if err != nil {
						t.Fatal(err)
					}
					freshRecorded, freshRecorder := fixture.InsertSelectRecordedPool(t, connector)
					freshConn, err := freshRecorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer freshConn.Close()
					if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal("fresh inherited CHECK Restore", err)
					}
					freshBackend := fixture.Backend{Recorder: freshRecorder}
					future(freshConn, freshBackend, true)
					check(freshConn, freshBackend)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal("Reset inherited CHECK Restore", err)
					}
					check(c, b)
					future(c, b, true)
					check(c, b)
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
							t.Fatal(err)
						}
						defer func() {
							if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
								t.Error(err)
							}
						}()
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("unmodified native inherited CHECK dump replay", err)
						}
						pgBackend := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
						pgConn, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pgConn.Close()
						future(pgConn, pgBackend, true)
						check(pgConn, pgBackend)
					}
				})
			})
		}
	}
}
