package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tr_ddl_saved")
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA tr_ddl_saved",
			"CREATE EXTENSION unaccent SCHEMA tr_ddl_saved",
			"CREATE EXTENSION fuzzystrmatch SCHEMA tr_ddl_saved",
			"CREATE TABLE tr_ddl_saved.data(id integer PRIMARY KEY,v integer)",
			"CREATE FUNCTION tr_ddl_saved.add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$",
			"CREATE FUNCTION tr_ddl_saved.twice() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v*2; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON tr_ddl_saved.data FOR EACH ROW EXECUTE FUNCTION tr_ddl_saved.add('3')",
			"CREATE TRIGGER z BEFORE INSERT ON tr_ddl_saved.data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tr_ddl_saved.twice()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO tr_ddl_saved.data VALUES(1,5)", 1))
		userOperatorExec(t, p, b, c,
			`ALTER TRIGGER z ON tr_ddl_saved.data RENAME TO "0 ' 名"`,
			`ALTER TRIGGER "0 ' 名" ON tr_ddl_saved.data DEPENDS ON EXTENSION unaccent`,
			"ALTER TRIGGER a ON tr_ddl_saved.data DEPENDS ON EXTENSION unaccent",
			"ALTER TRIGGER a ON tr_ddl_saved.data NO DEPENDS ON EXTENSION unaccent",
			"CREATE TABLE tr_ddl_saved.rich(id integer,v integer,mark text)",
			"CREATE FUNCTION tr_ddl_saved.rich_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; NEW.mark:=TG_NAME; RETURN NEW; END$$",
			"CREATE TRIGGER old BEFORE UPDATE OF v ON tr_ddl_saved.rich FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tr_ddl_saved.rich_fn('7')",
			`ALTER TRIGGER old ON tr_ddl_saved.rich RENAME TO "rich ' 名"`,
			"CREATE TABLE tr_ddl_saved.parent(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE tr_ddl_saved.leaf PARTITION OF tr_ddl_saved.parent FOR VALUES FROM (0) TO (100)",
			"CREATE TRIGGER local BEFORE INSERT ON tr_ddl_saved.parent FOR EACH ROW EXECUTE FUNCTION tr_ddl_saved.add('3')",
			"ALTER TRIGGER local ON tr_ddl_saved.leaf DEPENDS ON EXTENSION fuzzystrmatch",
			"ALTER TRIGGER local ON tr_ddl_saved.parent RENAME TO inherited")
		run(fixture.Exec("INSERT INTO tr_ddl_saved.rich VALUES(1,5,'base')", 1))
		q := "INSERT INTO tr_ddl_saved.data VALUES($1,5) RETURNING id,v"
		held := triggerDDLHeld(t, p, b, c, q)
		updateQ := "UPDATE tr_ddl_saved.rich SET v=5 WHERE id=1 RETURNING id,v,mark"
		update := triggerDDLHeld(t, p, b, c, updateQ)
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = c.QueryContext(p.Ctx, "SELECT id,v FROM tr_ddl_saved.data ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		check := func(conn *sql.Conn, backend fixture.Backend) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			r(triggerRows("SELECT id,v FROM tr_ddl_saved.data ORDER BY id", []any{int64(1), int64(16)}))
			r(triggerDDLX("tr_ddl_saved.data", "unaccent", 1))
			r(triggerDDLX("tr_ddl_saved.parent", "fuzzystrmatch", 0))
			r(triggerDDLX("tr_ddl_saved.leaf", "fuzzystrmatch", 1))
			r(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			s := triggerRows(q, []any{int64(99), int64(13)})
			s.Args = []any{99}
			if conn == c {
				held(s)
			} else {
				r(fixture.Prepared(s))
			}
			s = fixture.Select(updateQ, []string{"id", "v", "mark"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(12), "rich ' 名"}})
			if conn == c {
				update(s)
			} else {
				r(fixture.Prepared(s))
			}
			r(fixture.Exec("UPDATE tr_ddl_saved.rich SET id=id", 1))
			r(triggerRows("INSERT INTO tr_ddl_saved.parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
			r(fixture.Exec("ALTER TRIGGER inherited ON tr_ddl_saved.leaf NO DEPENDS ON EXTENSION fuzzystrmatch", 0))
			r(fixture.Exec("DROP EXTENSION unaccent RESTRICT", 0))
			r(triggerRows("INSERT INTO tr_ddl_saved.data VALUES(98,5) RETURNING id,v", []any{int64(98), int64(8)}))
			r(fixture.Exec("DROP EXTENSION fuzzystrmatch RESTRICT", 0))
			r(triggerRows("INSERT INTO tr_ddl_saved.leaf VALUES(2,5) RETURNING id,v", []any{int64(2), int64(8)}))
			r(fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tr_ddl_saved", check)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{dump.String() + `; ALTER TRIGGER absent ON tr_ddl_saved.data DEPENDS ON EXTENSION unaccent;`, dump.String() + `; ALTER TRIGGER "unterminated`} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid restore accepted")
			}
			check(c, b)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		check(c, b)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check(c, b)
		if !retained.Next() {
			t.Fatal("retained row missing", retained.Err())
		}
		var id, v int64
		if err := retained.Scan(&id, &v); err != nil || id != 1 || v != 16 {
			t.Fatal(id, v, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained row cardinality", retained.Err())
		}
	})
}
