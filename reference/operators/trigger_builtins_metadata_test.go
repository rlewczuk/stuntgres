package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBuiltinsMetadata(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		s := fixture.Select("SELECT oid::bigint,proname::text,pronargs::integer,prorettype::bigint,prosrc,proisstrict,provolatile::text,proparallel::text,procost::text,prokind::text,prolang=12 AS internal FROM pg_proc WHERE oid IN (1291,3752,3753) ORDER BY oid",
			[]string{"oid", "proname", "pronargs", "prorettype", "prosrc", "proisstrict", "provolatile", "proparallel", "procost", "prokind", "internal"},
			[]string{"INT8", "TEXT", "INT4", "INT8", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{
				{int64(1291), "suppress_redundant_updates_trigger", int64(0), int64(2279), "suppress_redundant_updates_trigger", true, "v", "s", "1", "f", true},
				{int64(3752), "tsvector_update_trigger", int64(0), int64(2279), "tsvector_update_trigger_byid", false, "v", "s", "1", "f", true},
				{int64(3753), "tsvector_update_trigger_column", int64(0), int64(2279), "tsvector_update_trigger_bycolumn", false, "v", "s", "1", "f", true},
			})
		run(s)
		run(fixture.Prepared(s))
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_meta(id integer,title text,cfg regconfig,v tsvector)",
			"CREATE TRIGGER explicit BEFORE INSERT OR UPDATE ON tb_meta FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')",
			"CREATE TRIGGER row_config BEFORE INSERT OR UPDATE ON tb_meta FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')",
			"CREATE TRIGGER suppress BEFORE UPDATE ON tb_meta FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()")
		s = fixture.Select("SELECT tgname::text,tgfoid::bigint,tgtype::integer,tgnargs::integer,encode(tgargs,'escape') AS args FROM pg_trigger WHERE tgrelid='tb_meta'::regclass ORDER BY tgname",
			[]string{"tgname", "tgfoid", "tgtype", "tgnargs", "args"}, []string{"TEXT", "INT8", "INT4", "INT4", "TEXT"}, [][]any{
				{"explicit", int64(3752), int64(23), int64(3), `v\000pg_catalog.simple\000title\000`},
				{"row_config", int64(3753), int64(23), int64(3), `v\000cfg\000title\000`},
				{"suppress", int64(1291), int64(19), int64(0), ""},
			})
		run(s)
		run(fixture.Prepared(s))
		// A user routine with the same spelling remains an ordinary integer function.
		userOperatorExec(t, p, b, c, "CREATE FUNCTION public.tsvector_update_trigger() RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
		run(fixture.Select("SELECT public.tsvector_update_trigger() AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Error("CREATE TRIGGER bad BEFORE INSERT ON tb_meta FOR EACH ROW EXECUTE FUNCTION public.tsvector_update_trigger()", "42P17"))
	})
}

func builtinReturningMetadata(b fixture.Backend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		names := []string{"id", "vector"}
		columns, err := rows.Columns()
		if err != nil || !slices.Equal(columns, names) {
			t.Fatalf("columns=%v err=%v want=%v", columns, err, names)
		}
		// The primary key is known non-NULL natively; the computed vector
		// and both pq columns retain unknown nullability.
		fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4", "TEXT"}, []bool{false, false}, 1)
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, column := range types {
			precision, scale, known := column.DecimalSize()
			if column.Name() != names[i] || precision != 0 || scale != 0 || known {
				t.Fatalf("column %d name=%q decimal=%d,%d/%t; want %q and unknown decimal", i, column.Name(), precision, scale, known, names[i])
			}
		}
	}
}

func TestReferenceTriggerBuiltinsPrepared(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_prepared(id integer PRIMARY KEY,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_prepared FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title')",
			"CREATE TRIGGER z BEFORE UPDATE ON tb_prepared FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tb_prepared VALUES(1,'Rats',NULL)", 1))
		q := "UPDATE tb_prepared SET title=$1 WHERE id=1 RETURNING id,v::text AS vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			text, vector string
			empty        bool
		}{{"Rats", "", true}, {"Cats", "'cats':1", false}, {"Cats", "", true}, {"The Fat", "'fat':2 'the':1", false}} {
			var rows [][]any
			if !tc.empty {
				rows = [][]any{{int64(1), tc.vector}}
			}
			s := builtinVectorRows(q, rows...)
			s.Args = []any{tc.text}
			s.CheckMetadata = builtinReturningMetadata(b)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, "Changed")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled held builtin update", err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, builtinVectorRows("SELECT id,v::text AS vector FROM tb_prepared", []any{int64(1), "'fat':2 'the':1"}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("PREPARE tb_sql(text) AS UPDATE tb_prepared SET title=$1 WHERE id=1 RETURNING id,v::text AS vector", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, builtinVectorRows("EXECUTE tb_sql('Rats')", []any{int64(1), "'rats':1"}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, builtinVectorRows("EXECUTE tb_sql('Rats')"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DEALLOCATE tb_sql", 0))
	})
}

func TestReferenceTriggerBuiltinsRepresentation(t *testing.T) {
	// NUMERIC scale is a representation witness, not a SQL equality test.
	// Confirm this independently reasoned witness on the pinned oracle first.
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_repr(id integer,n numeric)", "CREATE TRIGGER a BEFORE UPDATE ON tb_repr FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()")
		run(fixture.Exec("INSERT INTO tb_repr VALUES(1,1.0)", 1))
		run(fixture.Select("SELECT n=1.00 AS equal FROM tb_repr", []string{"equal"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("UPDATE tb_repr SET n=1.00", 1))
		run(fixture.Exec("UPDATE tb_repr SET n=1.00", 0))
		run(fixture.Select("SELECT n::text AS n FROM tb_repr", []string{"n"}, []string{"TEXT"}, [][]any{{"1.00"}}))
	})
}
