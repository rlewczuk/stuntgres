package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModePersistence(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		names := triggerModeFKSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE TRIGGER b BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()",
			"CREATE TRIGGER c BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()",
			"ALTER TABLE tm_data DISABLE TRIGGER a", "ALTER TABLE tm_data ENABLE REPLICA TRIGGER b", "ALTER TABLE tm_data ENABLE ALWAYS TRIGGER c",
			"CREATE TABLE tm_audit(label text)",
			"CREATE FUNCTION tm_saved_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_audit VALUES('fired'); RETURN NULL; END$$",
			"CREATE TRIGGER s AFTER INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_saved_audit()",
			"ALTER TABLE tm_data DISABLE TRIGGER s",
			"CREATE TABLE tm_part(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE tm_leaf PARTITION OF tm_part FOR VALUES FROM (0) TO (10)",
			"CREATE TABLE tm_other PARTITION OF tm_part FOR VALUES FROM (10) TO (20)",
			"CREATE TRIGGER a BEFORE INSERT ON tm_part FOR EACH ROW EXECUTE FUNCTION tm_add()",
			"ALTER TABLE tm_part ENABLE ALWAYS TRIGGER a",
			"ALTER TABLE ONLY tm_leaf ENABLE TRIGGER a", "ALTER TABLE ONLY tm_other DISABLE TRIGGER a",
			"ALTER TABLE tm_child DISABLE TRIGGER "+triggerModeQuote(names[2]),
			"ALTER TABLE tm_parent DISABLE TRIGGER "+triggerModeQuote(names[0]),
			"ALTER TABLE tm_parent ENABLE REPLICA TRIGGER "+triggerModeQuote(names[1]),
			"ALTER TABLE tm_child ENABLE ALWAYS TRIGGER "+triggerModeQuote(names[3]))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_child VALUES(1,999)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_parent VALUES(10)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_child VALUES(10,10)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_data VALUES(1,5)", 1))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		const savedMarkerText = "saved literal\n-- stuntgres-fk-trigger-mode-v2 !not-a-record!\nend literal"
		userOperatorExec(t, p, b, c, "CREATE TABLE tm_saved_text(v text)")
		run(fixture.Exec("INSERT INTO tm_saved_text VALUES($1)", 1, savedMarkerText))
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tm_origin_parent(id integer PRIMARY KEY)",
			"CREATE TABLE tm_origin_child(id integer,pid integer,CONSTRAINT tm_origin_fk FOREIGN KEY(pid) REFERENCES tm_origin_parent(id))",
			"SET session_replication_role=replica")
		run(fixture.Exec("INSERT INTO tm_origin_child VALUES(1,700)", 1))
		run(fixture.Exec("SET session_replication_role=origin", 0))
		q := "INSERT INTO tm_data VALUES($1,5) RETURNING id,v"
		held := triggerDDLHeld(t, p, b, c, q)
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = c.QueryContext(p.Ctx, "SELECT id,v FROM tm_data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		check := func() {
			run(triggerModeCatalog("tm_data", []any{"a", "D"}, []any{"b", "R"}, []any{"c", "A"}, []any{"s", "D"}))
			run(triggerModeCatalog("tm_part", []any{"a", "A"}))
			run(triggerModeCatalog("tm_leaf", []any{"a", "O"}))
			run(triggerModeCatalog("tm_other", []any{"a", "D"}))
			run(fixture.Select("SELECT c.relname,t.tgtype,t.tgenabled FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_constraint f ON f.oid=t.tgconstraint WHERE f.conname='tm_fk' ORDER BY c.relname,t.tgtype", []string{"relname", "tgtype", "tgenabled"}, []string{"NAME", "INT2", "CHAR"}, [][]any{{"tm_child", int64(5), "D"}, {"tm_child", int64(17), "A"}, {"tm_parent", int64(9), "D"}, {"tm_parent", int64(17), "R"}}))
			run(triggerRows("SELECT id,v FROM tm_data ORDER BY id", []any{int64(1), int64(105)}))
			run(fixture.Select("SELECT id,pid FROM tm_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(999)}, {int64(10), int64(10)}}))
			run(triggerModeCount("tm_audit", 0))
			run(fixture.Select("SELECT v FROM tm_saved_text", []string{"v"}, []string{"TEXT"}, [][]any{{savedMarkerText}}))
			run(fixture.Select("SELECT id,pid FROM tm_origin_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(700)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='tm_origin_fk') AND tgenabled='O'", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(fixture.ErrorDiagnostic("INSERT INTO tm_origin_child VALUES(2,701)", fixture.ForeignKeyChild("tm_origin_child", "tm_origin_fk", "Key (pid)=(701) is not present in table \"tm_origin_parent\".")))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			s := triggerRows(q, []any{int64(50), int64(105)})
			s.Args = []any{50}
			held(s)
			run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
			s = triggerRows(q, []any{int64(51), int64(205)})
			s.Args = []any{51}
			held(s)
			run(triggerRows("INSERT INTO tm_part VALUES(2,5),(12,5) RETURNING id,v", []any{int64(2), int64(5)}, []any{int64(12), int64(5)}))
			run(triggerModeFKError("UPDATE tm_child SET pid=998 WHERE id=1", 998))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DELETE FROM tm_parent WHERE id=10", 1))
			run(triggerModeCount("tm_child WHERE id=10", 1))
			run(fixture.Exec("INSERT INTO tm_child VALUES(2,998)", 1))
			run(fixture.Exec("ROLLBACK", 0))
		}
		check()
		if b.PG {
			return
		}
		var saved []byte
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if saved != nil && !bytes.Equal(saved, dump.Bytes()) {
				t.Fatal("mode snapshot redump changed after RI identity remint")
			}
			saved = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			check()
		}
		triggerModeRejectReplay(t, p, saved, check)
		for _, suffix := range []string{"; ALTER TABLE tm_data ENABLE TRIGGER absent;", "; ALTER TABLE tm_data ENABLE REPLICA TRIGGER ALL;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(saved)+suffix)); err == nil {
				t.Fatal("invalid late mode accepted")
			}
			check()
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: saved}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		check()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check()
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var id, v int64
		if err := retained.Scan(&id, &v); err != nil || id != 1 || v != 105 {
			t.Fatal(id, v, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal(retained.Err())
		}
	})
}

func TestReferenceTriggerModeFeatureSmoke(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		names := triggerModeFKSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		identitySQL := "SELECT oid::bigint,tgfoid::bigint FROM pg_trigger WHERE tgrelid='tm_data'::regclass AND tgname='a'"
		var originalOID, originalFunction int64
		originalDefinition := triggerModeDefinitions(t, p, c, "tm_data")
		if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&originalOID, &originalFunction); err != nil || originalOID <= 0 || originalFunction <= 0 {
			t.Fatal("trigger identity", err)
		}
		q := "INSERT INTO tm_data VALUES($1,5)"
		held := triggerDDLHeld(t, p, b, c, q)
		insert := func(id int64) { s := fixture.Exec(q, 1, id); held(s) }
		insert(1)
		run(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER a", 0))
		insert(2)
		run(fixture.Exec("ALTER TABLE tm_data ENABLE REPLICA TRIGGER a", 0))
		insert(3)
		run(fixture.Exec("SET session_replication_role=replica", 0))
		insert(4)
		run(fixture.Exec("ALTER TABLE tm_data ENABLE ALWAYS TRIGGER a", 0))
		insert(5)
		run(fixture.Exec("SET session_replication_role=local", 0))
		insert(6)
		run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER a", 0))
		insert(7)
		run(triggerDDLError("ALTER TABLE tm_data DISABLE TRIGGER a, ENABLE TRIGGER missing", "42704", `trigger "missing" for table "tm_data" does not exist`, false))
		insert(8)
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER a", 0))
		run(fixture.Exec("ROLLBACK", 0))
		insert(9)
		want := [][]any{{int64(1), int64(105)}, {int64(2), int64(5)}, {int64(3), int64(5)}, {int64(4), int64(105)}, {int64(5), int64(105)}, {int64(6), int64(105)}, {int64(7), int64(105)}, {int64(8), int64(105)}, {int64(9), int64(105)}}
		run(triggerRows("SELECT id,v FROM tm_data ORDER BY id", want...))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}))
		run(fixture.Exec("ALTER TABLE tm_child DISABLE TRIGGER ALL", 0))
		run(fixture.Exec("INSERT INTO tm_child VALUES(1,99)", 1))
		run(fixture.Exec("ALTER TABLE tm_child ENABLE TRIGGER ALL", 0))
		run(triggerModeFKError("INSERT INTO tm_child VALUES(2,98)", 98))
		run(fixture.Exec("INSERT INTO tm_parent VALUES(10),(20)", 2))
		run(fixture.Exec("INSERT INTO tm_child VALUES(10,10),(20,20)", 2))
		run(fixture.Exec("ALTER TABLE tm_parent DISABLE TRIGGER "+triggerModeQuote(names[0]), 0))
		run(fixture.Exec("DELETE FROM tm_parent WHERE id=10", 1))
		run(triggerModeCount("tm_child WHERE id=10", 1))
		run(fixture.Exec("ALTER TABLE tm_parent ENABLE TRIGGER "+triggerModeQuote(names[0]), 0))
		run(fixture.Exec("DELETE FROM tm_parent WHERE id=20", 1))
		run(triggerModeCount("tm_child WHERE id=20", 0))
		run(fixture.Exec("ALTER TABLE tm_data ENABLE ALWAYS TRIGGER a", 0))
		run(fixture.Exec("ALTER TABLE tm_child DISABLE TRIGGER "+triggerModeQuote(names[2]), 0))
		var currentOID, currentFunction int64
		currentDefinition := triggerModeDefinitions(t, p, c, "tm_data")
		if err := c.QueryRowContext(p.Ctx, identitySQL).Scan(&currentOID, &currentFunction); err != nil || currentOID != originalOID || currentFunction != originalFunction || !reflect.DeepEqual(currentDefinition, originalDefinition) {
			t.Fatal("mode changed trigger identity or definition", currentOID, currentFunction, currentDefinition, err)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(triggerModeCatalog("tm_data", []any{"a", "A"}))
			run(fixture.Select("SELECT id,pid FROM tm_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(10), int64(10)}}))
			run(triggerRows("SELECT id,v FROM tm_data ORDER BY id", want...))
			insert(10)
			run(triggerRows("SELECT id,v FROM tm_data WHERE id=10", []any{int64(10), int64(105)}))
			run(fixture.Exec("INSERT INTO tm_child VALUES(2,98)", 1))
		}
	})
}
