package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func deferredSavedAudit(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT op,id,ov,v,n,label,arg FROM ds.audit ORDER BY id,op", []string{"op", "id", "ov", "v", "n", "label", "arg"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT8", "TEXT", "TEXT"}, rows)
}
func deferredSavedCatalog() fixture.Step {
	return fixture.Select("SELECT t.tgname,c.conname,t.tgtype,t.tgdeferrable,t.tginitdeferred,c.contype,t.tgconstrrelid='ds.referenced'::regclass AS from_ref,c.confrelid,t.tgconstraint=c.oid AND c.oid<>t.oid AND c.conrelid=t.tgrelid AS linked FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='ds.data'::regclass ORDER BY t.tgname", []string{"tgname", "conname", "tgtype", "tgdeferrable", "tginitdeferred", "contype", "from_ref", "confrelid", "linked"}, []string{"NAME", "NAME", "INT2", "BOOL", "BOOL", "CHAR", "BOOL", "OID", "BOOL"}, [][]any{
		{"del", "del", int64(9), true, true, "t", false, "0", true},
		{"live ' quoted", "selected", int64(5), true, true, "t", true, "0", true},
		{"upd", "upd", int64(17), true, true, "t", false, "0", true},
	})
}
func deferredSavedClones() fixture.Step {
	return fixture.Select("SELECT r.relname,t.tgname,c.conname,c.conparentid,c.conislocal,c.coninhcount,c.connoinherit,t.tgparentid<>0 AS child,t.tgdeferrable,t.tginitdeferred,c.condeferrable,c.condeferred FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint JOIN pg_class r ON r.oid=t.tgrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname='ds' AND r.relname IN('root','leaf','later') ORDER BY r.relname", []string{"relname", "tgname", "conname", "conparentid", "conislocal", "coninhcount", "connoinherit", "child", "tgdeferrable", "tginitdeferred", "condeferrable", "condeferred"}, []string{"NAME", "NAME", "NAME", "OID", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"later", "inherited", "inherited", "0", true, int64(0), true, true, true, true, true, true}, {"leaf", "inherited", "clone_c", "0", true, int64(0), true, true, true, true, true, true}, {"root", "inherited", "root_selected", "0", true, int64(0), true, false, true, true, true, true}})
}

// The smoke deliberately shares the full public lifetime witness: it reaches
// savepoint consumption restoration, recursive SET/commit, rejected terminal
// publication, real catalogs and all native/released SQL replay phases.
func TestReferenceTriggerDeferredFeatureSmoke(t *testing.T) { deferredPersistence(t) }
func TestReferenceTriggerDeferredPersistence(t *testing.T)  { deferredPersistence(t) }

func deferredPersistence(t *testing.T) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ds", "dm", "dl")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA ds",
			"CREATE TABLE ds.data(id integer PRIMARY KEY,v integer)",
			"CREATE TABLE ds.audit(op text,id integer,ov integer,v integer,n bigint,label text,arg text)",
			"CREATE TABLE ds.referenced(id integer)",
			"CREATE TABLE ds.child(id integer PRIMARY KEY)",
			"CREATE TABLE ds.child_log(id integer)",
			"CREATE FUNCTION ds.row_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.audit VALUES(TG_OP,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v,(SELECT count(*) FROM ds.data),TG_NAME,TG_ARGV[0]); IF TG_OP<>'DELETE' THEN NEW.v:=999; END IF; RETURN NULL; END$$",
			"CREATE FUNCTION ds.child_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.child_log VALUES(NEW.id); RETURN NULL; END$$",
			"CREATE CONSTRAINT TRIGGER \"row ' quoted\" AFTER INSERT ON ds.data FROM ds.referenced DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION ds.row_event('x''y')",
			"ALTER TRIGGER \"row ' quoted\" ON ds.data RENAME TO \"live ' quoted\"",
			"ALTER TABLE ds.data RENAME CONSTRAINT \"row ' quoted\" TO selected",
			"CREATE CONSTRAINT TRIGGER upd AFTER UPDATE OF v ON ds.data DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION ds.row_event('update')",
			"CREATE CONSTRAINT TRIGGER del AFTER DELETE ON ds.data DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(OLD.v>0) EXECUTE FUNCTION ds.row_event('delete')",
			"CREATE CONSTRAINT TRIGGER cb AFTER INSERT ON ds.child DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.child_event()",
			"CREATE TABLE ds.fixed(id integer)",
			"CREATE CONSTRAINT TRIGGER fixed AFTER INSERT ON ds.fixed NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION ds.child_event()",
			"CREATE TABLE ds.root(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE ds.leaf PARTITION OF ds.root FOR VALUES FROM(0) TO(100)",
			"CREATE TABLE ds.clone_log(id integer,label text)",
			"CREATE FUNCTION ds.clone_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.clone_log VALUES(NEW.id,TG_TABLE_NAME||':'||TG_NAME); RETURN NULL; END$$",
			"CREATE CONSTRAINT TRIGGER clone_c AFTER INSERT ON ds.root DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.clone_event()",
			"ALTER TRIGGER clone_c ON ds.root RENAME TO inherited",
			"ALTER TABLE ds.root RENAME CONSTRAINT clone_c TO root_selected",
			"CREATE TABLE ds.later PARTITION OF ds.root FOR VALUES FROM(100) TO(200)")
		deferredSavedNamesSetup(t, p, b, c)
		deferredSavedDeepSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, deferredOIDCarriers(b, s)) }
		insert := "INSERT INTO ds.data VALUES($1,$2) RETURNING id,v"
		held, err := c.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		update := "UPDATE ds.data SET v=$1 WHERE id=$2 RETURNING id,v"
		u, err := c.PrepareContext(p.Ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		defer u.Close()
		remove := "DELETE FROM ds.data WHERE id=$1 RETURNING id,v"
		d, err := c.PrepareContext(p.Ctx, remove)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		setSQL := "SET CONSTRAINTS ds.selected IMMEDIATE"
		set, err := c.PrepareContext(p.Ctx, setSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer set.Close()
		run(deferredCount("ds.data", 0))
		run(deferredCount("ds.audit", 0))
		run(deferredSavedCatalog())
		run(deferredSavedClones())
		run(fixture.Exec("BEGIN", 0))
		s := triggerRows(insert, []any{int64(1), int64(10)})
		s.Args = []any{1, 10}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: insert}, b.Recorder, s)
		run(deferredCount("ds.audit", 0))
		run(fixture.Exec("SAVEPOINT s", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: set, SQL: setSQL}, b.Recorder, fixture.Exec(setSQL, 0))
		run(deferredCount("ds.audit", 1))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(deferredCount("ds.audit", 0))
		run(fixture.Exec("RELEASE s", 0))
		run(fixture.Exec("INSERT INTO ds.data VALUES(2,20)", 1))
		run(deferredCount("ds.audit", 0))
		run(fixture.Exec("COMMIT", 0))
		baseline := [][]any{{"INSERT", int64(1), nil, int64(10), int64(2), "live ' quoted", "x'y"}, {"INSERT", int64(2), nil, int64(20), int64(2), "live ' quoted", "x'y"}}
		run(deferredSavedAudit(baseline...))
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION ds.row_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.audit VALUES(TG_OP,COALESCE(NEW.id,OLD.id),OLD.v,NEW.v,(SELECT count(*) FROM ds.data),TG_NAME,TG_ARGV[0]); IF TG_OP='INSERT' THEN INSERT INTO ds.child VALUES(NEW.id); END IF; IF TG_OP<>'DELETE' THEN NEW.v:=999; END IF; RETURN NULL; END$$")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO ds.data VALUES(3,30)", 1)))
		run(deferredCount("ds.audit", 2))
		run(deferredCount("ds.child_log", 0))
		run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0)))
		run(deferredCount("ds.audit", 3))
		run(deferredCount("ds.child_log", 1))
		run(fixture.Exec("COMMIT", 0))
		baseline = append(baseline, []any{"INSERT", int64(3), nil, int64(30), int64(3), "live ' quoted", "x'y"})
		run(deferredSavedAudit(baseline...))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION ds.fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO ds.audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'failed','failed'); PERFORM 1/0; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER z_bad AFTER INSERT ON ds.data DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ds.fail()")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO ds.data VALUES(4,40)", 1))
		run(deferredFailure("COMMIT", "ds.fail"))
		run(triggerRows("SELECT id,v FROM ds.data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
		run(deferredSavedAudit(baseline...))
		run(deferredCount("ds.child_log", 1))
		run(fixture.Exec("DROP TRIGGER z_bad ON ds.data", 0))

		check := func(conn *sql.Conn, backend fixture.Backend) {
			step := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, deferredOIDCarriers(backend, s))
			}
			step(triggerRows("SELECT id,v FROM ds.data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
			step(deferredSavedAudit(baseline...))
			step(deferredSavedCatalog())
			step(deferredSavedClones())
			step(deferredCount("ds.child", 1))
			step(deferredCount("ds.child_log", 1))
			step(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgrelid='ds.fixed'::regclass AND tgname='fixed'", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
			step(fixture.Exec("BEGIN", 0))
			step(fixture.Prepared(fixture.Exec("INSERT INTO ds.fixed VALUES(77)", 1)))
			step(deferredCount("ds.child_log", 2))
			step(fixture.Exec("ROLLBACK", 0))
			step(deferredCount("ds.child_log", 1))
			deferredSavedNames(t, p, backend, conn)
			deferredSavedDeepCheck(t, p, backend, conn)
			step(fixture.Exec("BEGIN", 0))
			s := triggerRows(insert, []any{int64(9), int64(90)})
			s.Args = []any{9, 90}
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: insert}, backend.Recorder, s)
			} else {
				step(fixture.Prepared(s))
			}
			s = triggerRows(insert, []any{int64(8), int64(80)})
			s.Args = []any{8, 80}
			step(fixture.Prepared(s))
			step(deferredSavedAudit(baseline...))
			step(deferredCount("ds.child_log", 1))
			step(fixture.Prepared(fixture.Exec(setSQL, 0)))
			want := append([][]any(nil), baseline...)
			want = append(want, []any{"INSERT", int64(8), nil, int64(80), int64(5), "live ' quoted", "x'y"}, []any{"INSERT", int64(9), nil, int64(90), int64(5), "live ' quoted", "x'y"})
			step(deferredSavedAudit(want...))
			step(deferredCount("ds.child_log", 1))
			s = triggerRows(update, []any{int64(1), int64(11)})
			s.Args = []any{11, 1}
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: u, SQL: update}, backend.Recorder, s)
			} else {
				step(fixture.Prepared(s))
			}
			s = triggerRows(remove, []any{int64(2), int64(20)})
			s.Args = []any{2}
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: d, SQL: remove}, backend.Recorder, s)
			} else {
				step(fixture.Prepared(s))
			}
			step(deferredCount("ds.audit", 5))
			step(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			step(deferredCount("ds.child_log", 3))
			want = [][]any{{"INSERT", int64(1), nil, int64(10), int64(2), "live ' quoted", "x'y"}, {"UPDATE", int64(1), int64(10), int64(11), int64(4), "upd", "update"}, {"DELETE", int64(2), int64(20), nil, int64(4), "del", "delete"}, {"INSERT", int64(2), nil, int64(20), int64(2), "live ' quoted", "x'y"}, {"INSERT", int64(3), nil, int64(30), int64(3), "live ' quoted", "x'y"}, {"INSERT", int64(8), nil, int64(80), int64(5), "live ' quoted", "x'y"}, {"INSERT", int64(9), nil, int64(90), int64(5), "live ' quoted", "x'y"}}
			step(deferredSavedAudit(want...))
			step(fixture.Exec("INSERT INTO ds.root VALUES(1),(101)", 2))
			step(fixture.Select("SELECT id,label FROM ds.clone_log ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "leaf:inherited"}, {int64(101), "later:inherited"}}))
			step(fixture.Exec("ROLLBACK", 0))
			step(deferredSavedAudit(baseline...))
			step(deferredCount("ds.child_log", 1))
			step(deferredCount("ds.clone_log", 0))
			// A new incarnation uses declaration defaults, not the preceding ALL.
			step(fixture.Exec("BEGIN", 0))
			step(fixture.Exec("INSERT INTO ds.data VALUES(6,60)", 1))
			step(deferredSavedAudit(baseline...))
			step(fixture.Exec("ROLLBACK", 0))
		}
		if !b.PG {
			// Explicit native control: committed-only dump while a row/event and a
			// named mode override exist. Neither belongs to the replacement.
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO ds.data VALUES(99,990)", 1))
			run(fixture.Exec("SET CONSTRAINTS ds.upd IMMEDIATE", 0))
			run(deferredCount("ds.audit", 3))
			var committed bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ROLLBACK", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(committed.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(c, b)
		}
		// Two native Reset/Restore rounds; their unchanged SQL is replayed on PG.
		parametersReplay(t, p, b, c, "ds", check, "dm", "dl")
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		cb := fixture.Backend{Recorder: rec}
		check(clone, cb)
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, clone, rec, deferredOIDCarriers(cb, s)) }
		step(fixture.Exec("INSERT INTO ds.data VALUES(7,70)", 1))
		distinct := append([][]any(nil), baseline...)
		distinct = append(distinct, []any{"INSERT", int64(7), nil, int64(70), int64(4), "live ' quoted", "x'y"})
		old, err := clone.QueryContext(p.Ctx, "SELECT id,v FROM ds.data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		writer, err := clone.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		for _, kind := range []string{"late-declaration", "reader", "cancel"} {
			var err error
			switch kind {
			case "late-declaration":
				err = stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String()+"; CREATE OR REPLACE CONSTRAINT TRIGGER invalid AFTER INSERT ON ds.data FOR EACH ROW EXECUTE FUNCTION ds.row_event();"))
				fixture.State(t, err, "0A000")
				want := fixture.ControlsReferenceDiagnostic("0A000", "CREATE OR REPLACE CONSTRAINT TRIGGER is not supported")
				want.Position = fixture.Position(utf8.RuneCountInString(dump.String()) + 3)
				fixture.AssertDiagnostic(t, err, &want)
			case "reader":
				err = stuntgres.Restore(p.Ctx, raw, &plpgsqlFailReader{data: dump.Bytes()})
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatal(err)
				}
			case "cancel":
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				err = stuntgres.Restore(ctx, raw, bytes.NewReader(dump.Bytes()))
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if kind != "late-declaration" {
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatalf("%s Restore returned SQLSTATE %s for ordinary host error", kind, state.SQLState())
				}
			}
			step(triggerRows("SELECT id,v FROM ds.data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}, []any{int64(7), int64(70)}))
			step(deferredSavedAudit(distinct...))
			step(deferredSavedCatalog())
			step(deferredSavedClones())
			deferredSavedDeepCheck(t, p, cb, clone)
			deferredSavedNames(t, p, cb, clone)
			step(fixture.Exec("BEGIN", 0))
			s := triggerRows(insert, []any{int64(8), int64(80)})
			s.Args = []any{8, 80}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: clone, Statement: writer, SQL: insert}, rec, s)
			s = triggerRows(insert, []any{int64(9), int64(90)})
			s.Args = []any{9, 90}
			step(fixture.Prepared(s))
			step(deferredSavedAudit(distinct...))
			step(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			want := append([][]any(nil), distinct...)
			want = append(want, []any{"INSERT", int64(8), nil, int64(80), int64(6), "live ' quoted", "x'y"}, []any{"INSERT", int64(9), nil, int64(90), int64(6), "live ' quoted", "x'y"})
			step(deferredSavedAudit(want...))
			step(deferredCount("ds.child_log", 4))
			step(fixture.Exec("ROLLBACK", 0))
			step(deferredSavedAudit(distinct...))
			step(deferredCount("ds.child_log", 2))
		}
		for _, want := range [][2]int64{{1, 10}, {2, 20}, {3, 30}, {7, 70}} {
			if !old.Next() {
				t.Fatal("retained eager rows", old.Err())
			}
			var id, v int64
			if err := old.Scan(&id, &v); err != nil || id != want[0] || v != want[1] {
				t.Fatal(id, v, err)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained eager rows", old.Err())
		}
	})
}
