package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterControls(t *testing.T)   { compositeAlterControls(t, false) }
func TestReferenceCompositeAlterDumpReplay(t *testing.T) { compositeAlterControls(t, true) }

func compositeAlterControls(t *testing.T, replay bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text COLLATE "C")`, `CREATE DOMAIN wrapped AS pair`, `CREATE TYPE nested AS (r wrapped,a pair[])`, `CREATE TABLE holder(k integer,v pair,a pair[],d wrapped,n nested)`, `CREATE TABLE typed OF pair (id WITH OPTIONS DEFAULT 7, CONSTRAINT positive CHECK(id>0))`, `CREATE VIEW saved AS SELECT k,v FROM holder`)
				run(fixture.Exec(`INSERT INTO holder VALUES(1,$1,$2,$3,$4),(2,NULL,'{}',NULL,NULL)`, 2, "(7,x)", `[0:2]={"(7,x)",NULL,"(9,y)"}`, "(7,x)", `("(7,x)","{""(7,x)"",NULL}")`))
				run(fixture.Exec(`INSERT INTO typed VALUES(11,'y')`, 1))
				fixture.CompositeReferenceSetup(t, p, b, c, `ALTER TYPE pair ADD ATTRIBUTE extra text CASCADE`, `ALTER TYPE pair RENAME ATTRIBUTE note TO label CASCADE`, `ALTER TYPE pair DROP ATTRIBUTE label CASCADE`, `ALTER TYPE pair ADD ATTRIBUTE note text COLLATE "C" CASCADE`)
				check := func(observe fixture.Executor, backend fixture.Backend) {
					for _, q := range []string{`SELECT v FROM holder ORDER BY k`, `SELECT d AS v FROM holder ORDER BY k`, `SELECT v FROM saved ORDER BY k`} {
						fixture.CompositeReferenceRows(t, p, backend, observe, q, [][]any{{"(7,,)"}, {nil}}, prepared)
					}
					fixture.CompositeReferenceRows(t, p, backend, observe, `SELECT a AS v FROM holder ORDER BY k`, [][]any{{`[0:2]={"(7,,)",NULL,"(9,,)"}`}, {`{}`}}, prepared, "_PAIR")
					fixture.CompositeReferenceRows(t, p, backend, observe, `SELECT n AS v FROM holder ORDER BY k`, [][]any{{`("(7,,)","{""(7,,)"",NULL}")`}, {nil}}, prepared)
					s := fixture.Select(`SELECT * FROM typed`, []string{"id", "extra", "note"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(11), nil, nil}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, observe, backend.Recorder, s)
					s = fixture.Select(`SELECT reloftype='pair'::regtype::oid AS attached FROM pg_class WHERE oid='typed'::regclass::oid`, []string{"attached"}, []string{"BOOL"}, [][]any{{true}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, observe, backend.Recorder, s)
					s = fixture.Select(`SELECT attname,attcollation::bigint AS coll FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname", "coll"}, []string{"NAME", "INT8"}, [][]any{{"id", int64(0)}, {"extra", int64(100)}, {"note", int64(950)}})
					fixture.RunStep(t, p.Ctx, observe, backend.Recorder, s)
				}
				check(c, b)
				if b.PG {
					return
				}
				heldConn, err := p.Native.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer heldConn.Close()
				var held driver.Rows
				if err := heldConn.Raw(func(raw any) error {
					var e error
					held, e = raw.(driver.QueryerContext).QueryContext(p.Ctx, `SELECT v FROM holder ORDER BY k`, nil)
					return e
				}); err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if replay {
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
							t.Fatal("unmodified native emitted dump replay", err)
						}
						pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
						check(p.Postgres, pg)
						fixture.RunStep(t, p.Ctx, p.Postgres, pg.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO typed DEFAULT VALUES`, 1)))
						fixture.RunStep(t, p.Ctx, p.Postgres, pg.Recorder, fixture.Select(`SELECT id FROM typed ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(11)}}))
					}
					return
				}
				freshDriver := stuntgres.NewDriver()
				fresh := fixture.DriverPool(t, freshDriver, "composite-alter-fresh")
				connector, err := freshDriver.OpenConnector("composite-alter-fresh")
				if err != nil {
					t.Fatal(err)
				}
				freshRecorded, freshRecorder := fixture.InsertSelectRecordedPool(t, connector)
				for _, target := range []struct {
					control, observe *sql.DB
					recorder         *fixture.Recorder
				}{{fresh, freshRecorded, freshRecorder}, {p.Native, b.DB, b.Recorder}} {
					backend := b
					backend.Recorder = target.recorder
					if target.control == p.Native {
						if err := stuntgres.Reset(p.Ctx, target.control); err != nil {
							t.Fatal(err)
						}
					}
					if err := stuntgres.Restore(p.Ctx, target.control, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check(target.observe, backend)
					var redump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, target.control, &redump); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
						t.Errorf("canonical redump changed:\n%s\n%s", dump.String(), redump.String())
					}
					fixture.RunStep(t, p.Ctx, target.observe, backend.Recorder, fixture.Exec(`CREATE TABLE sentinel(v integer)`, 0))
					fixture.RunStep(t, p.Ctx, target.observe, backend.Recorder, fixture.Exec(`INSERT INTO sentinel VALUES(91)`, 1))
					for _, script := range []string{`CREATE TYPE replacement AS (i integer); CREATE TABLE fresh(v replacement); INSERT INTO fresh VALUES('(1)'),('(bad)');`, `CREATE TYPE replacement AS (i integer); CREATE TABLE fresh(v replacement); INSERT INTO fresh VALUES('`} {
						if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(script)); err == nil {
							t.Fatal("invalid Restore succeeded", script)
						}
						check(target.observe, backend)
					}
					if err := stuntgres.Restore(p.Ctx, target.control, compositeAlterBrokenReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if err := stuntgres.Restore(ctx, target.control, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					check(target.observe, backend)
					fixture.RunStep(t, p.Ctx, target.observe, backend.Recorder, fixture.Select(`SELECT v FROM sentinel`, []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					fixture.RunStep(t, p.Ctx, target.observe, backend.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO typed DEFAULT VALUES`, 1)))
					fixture.RunStep(t, p.Ctx, target.observe, backend.Recorder, fixture.Select(`SELECT id FROM typed ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(11)}}))
				}
				values := make([]driver.Value, 1)
				for _, want := range []driver.Value{"(7,,)", nil} {
					if err := held.Next(values); err != nil || values[0] != want {
						t.Fatal(values, want, err)
					}
				}
				if err := held.Next(values); err != io.EOF {
					t.Fatal(err)
				}
			})
		})
	}
}

type compositeAlterBrokenReader struct{}

func (compositeAlterBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
