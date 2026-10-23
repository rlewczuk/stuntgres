package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceInsertReturningControls(t *testing.T) {
	p := fixture.NewScenario(t)
	// Native control APIs have no server equivalents. Replay the actual native
	// dump on the real oracle below; do not replace it with handcrafted SQL.
	for _, q := range []string{"CREATE SEQUENCE s START 101 CACHE 1", "CREATE TABLE t(id INT PRIMARY KEY,v TEXT DEFAULT 'd',g INT GENERATED ALWAYS AS (id+1) STORED)", "CREATE VIEW live AS SELECT id,v FROM t"} {
		fixture.LockExec(t, p.Ctx, p.Native, q, 0)
	}
	fixture.DriverWantTuples(t, p.Native, "INSERT INTO t(id) VALUES(1) RETURNING old.id,new.id,v,g,nextval('s')", [][]any{{nil, int64(1), "d", int64(2), int64(101)}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "RETURNING") {
		t.Fatal("dump contains output history", dump.String())
	}
	clone := fixture.DriverPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
	if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.DriverWantTuples(t, clone, "INSERT INTO t(id) VALUES(2) RETURNING old,new,g,nextval('s')", [][]any{{nil, "(2,d,3)", int64(3), int64(102)}})
	held, err := clone.PrepareContext(p.Ctx, "INSERT INTO t(id,v) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING old,new")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	rows, err := held.QueryContext(p.Ctx, 1, "changed")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var old, newValue string
	if err = rows.Scan(&old, &newValue); err != nil || old != "(1,d,2)" || newValue != "(1,changed,2)" {
		t.Fatal(old, newValue, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	// An actual eager raw cursor and descriptors remain usable after replacement.
	raw, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	saved, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "INSERT INTO t(id) VALUES(3) RETURNING old,new,v", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	names := saved.Columns()
	if !reflect.DeepEqual(names, []string{"old", "new", "v"}) {
		t.Fatal(names)
	}
	checkSavedMetadata := func() {
		t.Helper()
		for i, want := range []string{"", "", "TEXT"} {
			if got := saved.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i); got != want {
				t.Fatal("retained type", i, got, want)
			}
			if got := saved.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i); got != reflect.TypeOf("") {
				t.Fatal("retained scan", i, got)
			}
		}
	}
	checkSavedMetadata()
	if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	checkSavedMetadata()
	values := make([]driver.Value, 3)
	if err = saved.Next(values); err != nil || !reflect.DeepEqual(values, []driver.Value{nil, "(3,d,4)", "d"}) {
		t.Fatal(values, err)
	}
	if !reflect.DeepEqual(saved.Columns(), names) {
		t.Fatal("held labels changed")
	}
	if err = saved.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	saved.Close()
	fixture.DriverWantTuples(t, p.Native, "INSERT INTO t(id) VALUES(4) RETURNING old,new", [][]any{{nil, "(4,d,5)"}})
	// This implementation-owned whitelist diagnostic is not a PostgreSQL API.
	script := "CREATE TABLE replacement(id INT); INSERT INTO replacement VALUES(1); INSERT INTO replacement VALUES(2) RETURNING id;"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, clone, strings.NewReader(script)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT RETURNING is unsupported"})
	fixture.DriverWantTuples(t, clone, "SELECT id,v,g FROM t ORDER BY id", [][]any{{int64(1), "changed", int64(2)}, {int64(2), "d", int64(3)}})
	if p.Postgres != nil {
		if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("INSERT INTO t(id) VALUES(2) RETURNING old.id,new.id,v,g,nextval('s')", []string{"id", "id", "v", "g", "nextval"}, []string{"INT4", "INT4", "TEXT", "INT4", "INT8"}, [][]any{{nil, int64(2), "d", int64(3), int64(102)}}))
		s := fixture.Select("INSERT INTO t(id,v) VALUES(1,'changed') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING old,new", []string{"old", "new"}, []string{"", ""}, [][]any{{"(1,d,2)", "(1,changed,2)"}})
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, insertReturningComposite(t, fixture.Backend{PG: true}, s))
	}
}
func TestReferenceInsertReturningConcurrency(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "updated", true: "filtered"}[skip], func(t *testing.T) {
			insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				a, worker := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
				var holderPID, workerPID int
				if b.PG {
					holderPID = fixture.LockPID(t, p.Ctx, a)
					workerPID = fixture.LockPID(t, p.Ctx, worker)
				}
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "UPDATE t SET v=40 WHERE id=1", 1)
				q := "INSERT INTO t VALUES(1,20) ON CONFLICT(id) DO UPDATE SET v=t.v+excluded.v"
				if skip {
					q += " WHERE t.v<30"
				}
				q += " RETURNING old.v,new.v,v"
				// Prepare before launching, then cleanup/join via the existing fixture.
				var x fixture.Executor = worker
				if prepared {
					held, e := worker.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer held.Close()
					x = fixture.HeldStatement{Executor: worker, Statement: held, SQL: q}
				}
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					rows, e := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: e}
				})
				if b.PG {
					bounded, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					fixture.RowWait(t, bounded, workerPID, holderPID)
				}
				// Native has no public queue observer; the private row-recheck unit added in
				// Phase B proves actual pending admission. Do not claim this launch as proof.
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				o := await()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				defer o.Rows.Close()
				if skip {
					if o.Rows.Next() {
						t.Fatal("filtered action returned a row")
					}
				} else {
					if !o.Rows.Next() {
						t.Fatal(o.Rows.Err())
					}
					var old, newValue, current int64
					if err = o.Rows.Scan(&old, &newValue, &current); err != nil || old != 40 || newValue != 60 || current != 60 {
						t.Fatal(old, newValue, current, err)
					}
					if o.Rows.Next() {
						t.Fatal("extra action")
					}
				}
				if o.Rows.Err() != nil {
					t.Fatal(o.Rows.Err())
				}
				o.Rows.Close()
				want := int64(60)
				if skip {
					want = 40
				}
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{want}}))
			})
		})
	}
}
func TestReferenceInsertReturningConditionalTriggers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
			run(fixture.Exec("CREATE TABLE triggered(id INT,v INT)", 0))
			definition := "CREATE FUNCTION adjust_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=0 THEN RETURN NULL; END IF; NEW.v:=NEW.v+100; RETURN NEW; END $$"
			run(fixture.Exec(definition, 0))
			run(fixture.Exec("CREATE TRIGGER adjust BEFORE INSERT ON triggered FOR EACH ROW EXECUTE FUNCTION adjust_insert()", 0))
			run(fixture.Select("INSERT INTO triggered VALUES(1,10),(0,20) RETURNING old.v,new.v,v", []string{"v", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, int64(110), int64(110)}}))
			run(fixture.Exec("INSERT INTO triggered VALUES(0,30) RETURNING *", 0))
			run(fixture.Exec("INSERT INTO triggered VALUES(2,40) RETURNING *", 1))
			run(fixture.Select("SELECT id,v FROM triggered ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(110)}, {int64(2), int64(140)}}))
		})
	}
}

func TestReferenceInsertReturningTriggers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
			run(fixture.Exec("CREATE TABLE triggered(id INT,v INT)", 0))
			run(fixture.Exec("CREATE FUNCTION adjust_insert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN CASE WHEN NEW.id=0 THEN NULL ELSE NEW END; END$$", 0))
			run(fixture.Exec("CREATE TRIGGER adjust BEFORE INSERT ON triggered FOR EACH ROW EXECUTE FUNCTION adjust_insert()", 0))
			run(fixture.Select("INSERT INTO triggered VALUES(1,10),(0,20) RETURNING old.v,new.v,v", []string{"v", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{nil, int64(110), int64(110)}}))
			run(fixture.Exec("INSERT INTO triggered VALUES(0,30) RETURNING *", 0))
			run(fixture.Exec("INSERT INTO triggered VALUES(2,40) RETURNING *", 1))
			run(fixture.Select("SELECT id,v FROM triggered ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(110)}, {int64(2), int64(140)}}))
		})
	}
}
