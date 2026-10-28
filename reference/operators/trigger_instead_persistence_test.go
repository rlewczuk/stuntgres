package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func insteadPersistence(t *testing.T, smoke bool) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "io_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA io_saved",
			`CREATE TABLE io_saved.data(id integer PRIMARY KEY,v integer)`,
			`CREATE TABLE io_saved.audit(op text,id integer,old_v integer,new_v integer)`,
			`CREATE VIEW io_saved."View ' quoted" AS SELECT id,v FROM io_saved.data`,
			`CREATE TABLE io_saved.chain_audit(name text,op text,id integer,v integer)`,
			`CREATE VIEW io_saved.gate AS SELECT id,v FROM io_saved.data`,
			`CREATE FUNCTION io_saved."body ' quoted"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP='INSERT' THEN INSERT INTO io_saved.data VALUES(NEW.id,NEW.v+1); ELSIF TG_OP='UPDATE' THEN UPDATE io_saved.data SET v=NEW.v+1 WHERE id=OLD.id; ELSE DELETE FROM io_saved.data WHERE id=OLD.id; END IF; INSERT INTO io_saved.audit VALUES(TG_OP,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v); IF TG_OP='DELETE' THEN OLD.v:=999; RETURN OLD; END IF; NEW.v:=NEW.v+100; RETURN NEW; END$$`,
			`CREATE FUNCTION io_saved.chain() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_NARGS<>1 OR TG_ARGV[0]<>'x''y' THEN PERFORM 1/0; END IF; INSERT INTO io_saved.chain_audit VALUES(TG_NAME,TG_OP,COALESCE(NEW.id,OLD.id),COALESCE(NEW.v,OLD.v)); RETURN COALESCE(NEW,OLD); END$$`,
			`CREATE FUNCTION io_saved.unchain() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_saved.chain_audit VALUES(TG_NAME,TG_OP,COALESCE(NEW.id,OLD.id),COALESCE(NEW.v,OLD.v)); RETURN COALESCE(NEW,OLD); END$$`,
			`CREATE TRIGGER z INSTEAD OF INSERT OR UPDATE OR DELETE ON io_saved."View ' quoted" FOR EACH ROW EXECUTE FUNCTION io_saved.unchain()`,
			`CREATE TRIGGER "body ' quoted" INSTEAD OF INSERT OR UPDATE OR DELETE ON io_saved."View ' quoted" FOR EACH ROW EXECUTE FUNCTION io_saved."body ' quoted"('persisted')`,
			`CREATE TRIGGER a INSTEAD OF INSERT OR UPDATE OR DELETE ON io_saved."View ' quoted" FOR EACH ROW EXECUTE FUNCTION io_saved.chain('x''y')`,
			`CREATE FUNCTION io_saved.suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$`,
			`CREATE TRIGGER body INSTEAD OF INSERT ON io_saved.gate FOR EACH ROW EXECUTE FUNCTION io_saved."body ' quoted"()`,
			`CREATE TRIGGER z INSTEAD OF INSERT ON io_saved.gate FOR EACH ROW EXECUTE FUNCTION io_saved.suppress()`)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		qs := []string{`INSERT INTO io_saved."View ' quoted" VALUES($1,$2) RETURNING id,v`, `UPDATE io_saved."View ' quoted" SET v=$2 WHERE id=$1 RETURNING id,v`, `DELETE FROM io_saved."View ' quoted" WHERE id=$1 RETURNING id,v`}
		handles := make([]*sql.Stmt, 3)
		for i, q := range qs {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			handles[i] = stmt
		}
		heldByConnection := map[*sql.Conn][]*sql.Stmt{c: handles}
		run(fixture.Select("SELECT count(*) AS n FROM io_saved.audit", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec(`INSERT INTO io_saved."View ' quoted" VALUES(1,10),(2,20)`, 2))
		saved := [][]any{{"INSERT", int64(1), nil, int64(10)}, {"INSERT", int64(2), nil, int64(20)}}
		logs := func(rows ...[]any) fixture.Step {
			s := fixture.Select("SELECT op,id,old_v,new_v FROM io_saved.audit", []string{"op", "id", "old_v", "new_v"}, []string{"TEXT", "INT4", "INT4", "INT4"}, rows)
			s.Bag = true
			return s
		}
		metadata := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT trigger_name,event_manipulation,action_timing FROM information_schema.triggers WHERE event_object_schema='io_saved' AND event_object_table='View '' quoted' ORDER BY event_manipulation,action_order", []string{"trigger_name", "event_manipulation", "action_timing"}, []string{"NAME", "VARCHAR", "VARCHAR"}, [][]any{{"a", "DELETE", "INSTEAD OF"}, {"body ' quoted", "DELETE", "INSTEAD OF"}, {"z", "DELETE", "INSTEAD OF"}, {"a", "INSERT", "INSTEAD OF"}, {"body ' quoted", "INSERT", "INSTEAD OF"}, {"z", "INSERT", "INSTEAD OF"}, {"a", "UPDATE", "INSTEAD OF"}, {"body ' quoted", "UPDATE", "INSTEAD OF"}, {"z", "UPDATE", "INSTEAD OF"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, insteadFlags("SELECT table_name,is_updatable,is_insertable_into,is_trigger_updatable,is_trigger_deletable,is_trigger_insertable_into FROM information_schema.views WHERE table_schema='io_saved' AND table_name='View '' quoted'", []any{"View ' quoted", "YES", "YES", "YES", "YES", "YES"}))
		}
		checkState := func(conn *sql.Conn, backend fixture.Backend, base, audit [][]any) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, triggerRows("SELECT id,v FROM io_saved.data ORDER BY id", base...))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, logs(audit...))
			metadata(conn, backend)
		}
		base := [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}
		exercise := func(conn *sql.Conn, backend fixture.Backend, initial, audit [][]any) {
			checkState(conn, backend, initial, audit)
			chainRows := [][]any{}
			for _, entry := range audit {
				chainRows = append(chainRows, []any{"a", entry[0], entry[1], entry[3]}, []any{"z", entry[0], entry[1], entry[3].(int64) + 100})
			}
			chainCheck := func(rows [][]any) {
				s := fixture.Select("SELECT name,op,id,v FROM io_saved.chain_audit", []string{"name", "op", "id", "v"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, rows)
				s.Bag = true
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			chainCheck(chainRows)
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			r(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			call := func(i int, want [][]any, args ...any) {
				s := triggerRows(qs[i], want...)
				s.Args = args
				if retained := heldByConnection[conn]; retained != nil {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: retained[i], SQL: qs[i]}, backend.Recorder, s)
				} else {
					r(fixture.Prepared(s))
				}
			}
			call(0, [][]any{{int64(9), int64(140)}}, 9, 40)
			s := triggerRows(qs[0], []any{int64(8), int64(130)})
			s.Args = []any{8, 30}
			r(fixture.Prepared(s))
			id, old := int64(1), int64(11)
			deleteID, deleteOld := int64(2), int64(21)
			if len(initial) == 1 {
				id = 77
				old = 777
				deleteID = 8
				deleteOld = 31
			}
			call(1, [][]any{{id, int64(115)}}, id, 15)
			call(2, [][]any{{deleteID, deleteOld}}, deleteID)
			expected := [][]any{{id, int64(16)}, {int64(8), int64(31)}, {int64(9), int64(41)}}
			if id == 77 {
				expected = [][]any{{int64(9), int64(41)}, {int64(77), int64(16)}}
			}
			want := append([][]any(nil), audit...)
			want = append(want, []any{"INSERT", int64(9), nil, int64(40)}, []any{"INSERT", int64(8), nil, int64(30)}, []any{"UPDATE", id, old, int64(15)}, []any{"DELETE", deleteID, deleteOld, nil})
			checkState(conn, backend, expected, want)
			chainWant := append([][]any(nil), chainRows...)
			chainWant = append(chainWant, []any{"a", "INSERT", int64(9), int64(40)}, []any{"z", "INSERT", int64(9), int64(140)}, []any{"a", "INSERT", int64(8), int64(30)}, []any{"z", "INSERT", int64(8), int64(130)}, []any{"a", "UPDATE", id, int64(15)}, []any{"z", "UPDATE", id, int64(115)}, []any{"a", "DELETE", deleteID, deleteOld}, []any{"z", "DELETE", deleteID, deleteOld})
			chainCheck(chainWant)
			r(fixture.Exec("ROLLBACK", 0))
			checkState(conn, backend, initial, audit)
			chainCheck(chainRows)
			r(fixture.Exec("BEGIN", 0))
			r(fixture.Prepared(triggerRows("INSERT INTO io_saved.gate VALUES(7,70) RETURNING id,v")))
			r(triggerRows("SELECT id,v FROM io_saved.data WHERE id=7", []any{int64(7), int64(71)}))
			nullAudit := append([][]any(nil), audit...)
			nullAudit = append(nullAudit, []any{"INSERT", int64(7), nil, int64(70)})
			r(logs(nullAudit...))
			r(fixture.Exec("ROLLBACK", 0))
			r(fixture.Exec("BEGIN", 0))
			r(fixture.Exec("INSERT INTO io_saved.gate VALUES(7,70)", 0))
			r(triggerRows("SELECT id,v FROM io_saved.data WHERE id=7", []any{int64(7), int64(71)}))
			r(fixture.Exec("ROLLBACK", 0))
			checkState(conn, backend, initial, audit)
			for _, prepared := range []bool{false, true} {
				route := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					r(s)
				}
				r(fixture.Exec("BEGIN", 0))
				route(fixture.Exec(`INSERT INTO io_saved."View ' quoted" VALUES(6,50)`, 1))
				route(fixture.Exec(`UPDATE io_saved."View ' quoted" SET v=60 WHERE id=6`, 1))
				route(fixture.Exec(`DELETE FROM io_saved."View ' quoted" WHERE id=6`, 1))
				expectedAudit := append([][]any(nil), audit...)
				expectedAudit = append(expectedAudit, []any{"INSERT", int64(6), nil, int64(50)}, []any{"UPDATE", int64(6), int64(51), int64(60)}, []any{"DELETE", int64(6), int64(61), nil})
				checkState(conn, backend, initial, expectedAudit)
				r(fixture.Exec("ROLLBACK", 0))
				checkState(conn, backend, initial, audit)
			}
		}
		if smoke {
			// Execute real reverse-created transformed chains and nested AFTER rollback,
			// not just startup/prepare. Transactions preserve the fixed saved snapshot.
			userOperatorExec(t, p, b, c, "BEGIN", `CREATE OR REPLACE FUNCTION io_saved.chain() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP<>'DELETE' THEN NEW.v:=NEW.v+3; RETURN NEW; END IF; RETURN OLD; END$$`, `CREATE OR REPLACE FUNCTION io_saved.unchain() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP<>'DELETE' THEN NEW.v:=NEW.v*2; RETURN NEW; END IF; RETURN OLD; END$$`)
			s := triggerRows(qs[0], []any{int64(6), int64(216)})
			s.Args = []any{6, 5}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: handles[0], SQL: qs[0]}, b.Recorder, s)
			run(triggerRows("SELECT id,v FROM io_saved.data WHERE id=6", []any{int64(6), int64(9)}))
			s = triggerRows(qs[1], []any{int64(6), int64(226)})
			s.Args = []any{6, 10}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: handles[1], SQL: qs[1]}, b.Recorder, s)
			s = triggerRows(qs[2], []any{int64(6), int64(14)})
			s.Args = []any{6}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: handles[2], SQL: qs[2]}, b.Recorder, s)
			userOperatorExec(t, p, b, c, "ROLLBACK", "CREATE FUNCTION io_saved.fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$", "CREATE TRIGGER fail AFTER INSERT ON io_saved.data FOR EACH ROW EXECUTE FUNCTION io_saved.fail()")
			run(fixture.Error(`INSERT INTO io_saved."View ' quoted" VALUES(6,5)`, "22012"))
			checkState(c, b, base, saved)
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_saved.fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(`INSERT INTO io_saved."View ' quoted" VALUES(6,5)`, 1))
			run(triggerRows("SELECT id,v FROM io_saved.data WHERE id=6", []any{int64(6), int64(6)}))
			run(fixture.Exec("ROLLBACK", 0))
		}
		check := func(conn *sql.Conn, backend fixture.Backend) { exercise(conn, backend, base, saved) }
		parametersReplay(t, p, b, c, "io_saved", check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		connector, err := stuntgres.NewDriver().OpenConnector("instead-fresh")
		if err != nil {
			t.Fatal(err)
		}
		cloneRaw := sql.OpenDB(connector)
		defer cloneRaw.Close()
		clonePool, cloneRecorder := fixture.RecordedNativePool(t, connector)
		cloneBackend := fixture.Backend{Name: "fresh-native", DB: clonePool, Recorder: cloneRecorder}
		clone, err := clonePool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		cloneRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, clone, cloneRecorder, s) }
		if err := stuntgres.Restore(p.Ctx, cloneRaw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		cloneHandles := make([]*sql.Stmt, len(qs))
		for i, q := range qs {
			stmt, err := clone.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			cloneHandles[i] = stmt
		}
		heldByConnection[clone] = cloneHandles
		check(clone, cloneBackend)
		checkState(c, b, base, saved)
		// Distinct destination data, executable definitions and eager results survive
		// until the second replacement, while the original engine remains unchanged.
		cloneRun(fixture.Exec("DELETE FROM io_saved.data", 2))
		cloneRun(fixture.Exec("DELETE FROM io_saved.audit", 2))
		cloneRun(fixture.Exec("DELETE FROM io_saved.chain_audit", 4))
		cloneRun(fixture.Exec("INSERT INTO io_saved.data VALUES(77,777)", 1))
		cloneRun(fixture.Exec(`CREATE OR REPLACE FUNCTION io_saved."body ' quoted"() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP='INSERT' THEN INSERT INTO io_saved.data VALUES(NEW.id,NEW.v+9); ELSIF TG_OP='UPDATE' THEN UPDATE io_saved.data SET v=NEW.v+9 WHERE id=OLD.id; ELSE DELETE FROM io_saved.data WHERE id=OLD.id; RETURN OLD; END IF; NEW.v:=NEW.v+900; RETURN NEW; END$$`, 0))
		cloneRun(fixture.Exec("BEGIN", 0))
		cloneProbe := triggerRows(qs[0], []any{int64(6), int64(901)})
		cloneProbe.Args = []any{6, 1}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: clone, Statement: cloneHandles[0], SQL: qs[0]}, cloneRecorder, cloneProbe)
		cloneRun(triggerRows("SELECT id,v FROM io_saved.data ORDER BY id", []any{int64(6), int64(10)}, []any{int64(77), int64(777)}))
		cloneRun(fixture.Exec("ROLLBACK", 0))
		checkState(c, b, base, saved)
		cloneOld, err := clone.QueryContext(p.Ctx, "SELECT id,v FROM io_saved.data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer cloneOld.Close()
		oldColumns, err := cloneOld.Columns()
		if err != nil || !reflect.DeepEqual(oldColumns, []string{"id", "v"}) {
			t.Fatal(oldColumns, err)
		}
		oldTypes, err := cloneOld.ColumnTypes()
		if err != nil || len(oldTypes) != 2 || oldTypes[0].DatabaseTypeName() != "INT4" || oldTypes[1].DatabaseTypeName() != "INT4" {
			t.Fatal(oldTypes, err)
		}
		oldColumns[0] = "caller-owned"
		if err := stuntgres.Restore(p.Ctx, cloneRaw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(clone, cloneBackend)
		checkState(c, b, base, saved)
		oldColumns, err = cloneOld.Columns()
		if err != nil || !reflect.DeepEqual(oldColumns, []string{"id", "v"}) {
			t.Fatal(oldColumns, err)
		}
		if !cloneOld.Next() {
			t.Fatal("lost clone eager row", cloneOld.Err())
		}
		var cloneID, cloneV int64
		if err := cloneOld.Scan(&cloneID, &cloneV); err != nil || cloneID != 77 || cloneV != 777 {
			t.Fatal(cloneID, cloneV, err)
		}
		if cloneOld.Next() || cloneOld.Err() != nil {
			t.Fatal("clone eager suffix", cloneOld.Err())
		}
		cloneOld.Close()
		// Distinct destination state proves each failed replacement is atomic.
		run(fixture.Exec("DELETE FROM io_saved.data", 2))
		run(fixture.Exec("DELETE FROM io_saved.audit", 2))
		run(fixture.Exec("INSERT INTO io_saved.data VALUES(77,777)", 1))
		run(fixture.Exec("DELETE FROM io_saved.chain_audit", 4))
		sentinel := [][]any{{int64(77), int64(777)}}
		failures := []func() error{
			func() error {
				return stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+`; CREATE TRIGGER bad INSTEAD OF INSERT ON io_saved."View ' quoted" FOR EACH ROW EXECUTE FUNCTION io_saved.missing();`))
			},
			func() error { return stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}) },
			func() error {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				return stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
			},
		}
		for i, fail := range failures {
			old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM io_saved.data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			cols, err := old.Columns()
			if err != nil {
				old.Close()
				t.Fatal(err)
			}
			types, err := old.ColumnTypes()
			if err != nil {
				old.Close()
				t.Fatal(err)
			}
			err = fail()
			if err == nil {
				old.Close()
				t.Fatal("invalid restore accepted", i)
			}
			if i == 1 && !errors.Is(err, io.ErrUnexpectedEOF) {
				old.Close()
				t.Fatal(err)
			}
			if i == 2 && !errors.Is(err, context.Canceled) {
				old.Close()
				t.Fatal(err)
			}
			exercise(c, b, sentinel, nil)
			if !reflect.DeepEqual(cols, []string{"id", "v"}) || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "INT4" {
				old.Close()
				t.Fatal("detached metadata", cols, types)
			}
			if !old.Next() {
				old.Close()
				t.Fatal("lost eager rows", old.Err())
			}
			var id, v int64
			if err := old.Scan(&id, &v); err != nil || id != 77 || v != 777 {
				old.Close()
				t.Fatal(id, v, err)
			}
			if old.Next() || old.Err() != nil {
				old.Close()
				t.Fatal(old.Err())
			}
			old.Close()
		}
		// A successful replacement must not mutate previously returned eager rows.
		old, err := c.QueryContext(p.Ctx, "SELECT id,v FROM io_saved.data")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var id, v int64
		if err := old.Scan(&id, &v); err != nil || id != 77 || v != 777 {
			t.Fatal(id, v, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
	})
}
func TestReferenceTriggerInsteadPersistence(t *testing.T)  { insteadPersistence(t, false) }
func TestReferenceTriggerInsteadFeatureSmoke(t *testing.T) { insteadPersistence(t, true) }
