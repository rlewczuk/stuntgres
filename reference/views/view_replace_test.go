package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceDefinition(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,txt TEXT)", 0), fixture.Exec("CREATE TABLE u(id INT,txt TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(1,'a')", 1), fixture.Exec("INSERT INTO u VALUES(2,'b')", 1), fixture.Exec("CREATE VIEW v AS SELECT id,txt FROM t", 0), fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0),
	} {
		p.Step(t, s)
	}
	for _, backend := range p.Backends() {
		db := backend.DB
		var oid, rule int64
		if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		retained, err := db.PrepareContext(p.Ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { retained.Close() })
		r, err := db.ExecContext(p.Ctx, "CREATE OR REPLACE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id+10 AS id,txt,TRUE AS extra FROM u")
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
		var gotOID, gotRule int64
		if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&gotOID); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&gotRule); err != nil || gotOID != oid || gotRule != rule {
			t.Fatal(gotOID, gotRule, oid, rule, err)
		}
		var id int64
		if err := retained.QueryRowContext(p.Ctx).Scan(&id); err != nil || id != 12 {
			t.Fatal(id, err)
		}
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT * FROM v", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(12), "b", true}}), fixture.Select("SELECT * FROM v WHERE FALSE", []string{"id", "txt", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{}), fixture.Select("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}), fixture.Exec("DROP TABLE t", 0), fixture.Exec("INSERT INTO u VALUES(3,'c')", 1), fixture.Select("SELECT id FROM w ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}, {int64(13)}}),
		fixture.Exec("CREATE OR REPLACE VIEW absent AS SELECT id FROM u", 0), fixture.Exec("CREATE SEQUENCE seq", 0), fixture.Exec("CREATE VIEW sv AS SELECT nextval('seq') AS n FROM u", 0), fixture.Exec("CREATE OR REPLACE VIEW sv AS SELECT nextval('seq') AS n FROM u WHERE FALSE", 0), fixture.Select("SELECT last_value,is_called FROM seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
	} {
		p.Step(t, s)
	}
}

func TestReferenceViewReplaceDiagnostics(t *testing.T) {
	cases := []struct {
		setup, query string
		want         fixture.Diagnostic
	}{
		{"", "CREATE OR REPLACE VIEW v AS SELECT id FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot drop columns from view"}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id AS renamed,txt FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change name of view column \"id\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id::BIGINT AS id,txt FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"id\" from integer to bigint"}},
		{"CREATE VIEW cv AS SELECT txt::VARCHAR(3) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt::VARCHAR(4) AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from character varying(3) to character varying(4)"}},
		{"CREATE VIEW cv AS SELECT id::NUMERIC(10,1) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT id::NUMERIC(10,2) AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from numeric(10,1) to numeric(10,2)"}},
		{"CREATE VIEW cv AS SELECT txt COLLATE \"C\" AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE \"POSIX\" AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change collation of view column \"x\" from \"C\" to \"POSIX\""}},
		{"", "CREATE OR REPLACE VIEW t AS SELECT id FROM t", fixture.Diagnostic{Code: "42809", Message: "\"t\" is not a view"}},
		{"", "ALTER VIEW t RENAME TO other", fixture.Diagnostic{Code: "42809", Message: "\"t\" is not a view"}},
		{"", "DROP VIEW t", fixture.Diagnostic{Code: "42809", Message: "\"t\" is not a view", Hint: "Use DROP TABLE to remove a table."}},
		{"", "DROP VIEW IF EXISTS t", fixture.Diagnostic{Code: "42809", Message: "\"t\" is not a view", Hint: "Use DROP TABLE to remove a table."}},
		{"", "DROP TABLE v", fixture.Diagnostic{Code: "42809", Message: "\"v\" is not a table", Hint: "Use DROP VIEW to remove a view."}},
		{"", "ALTER VIEW v RENAME id TO txt", fixture.Diagnostic{Code: "42701", Message: "column \"txt\" of relation \"v\" already exists"}},
		{"", "ALTER VIEW v RENAME COLUMN missing TO z", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist"}},
		{"", "ALTER VIEW v RENAME TO t", fixture.Diagnostic{Code: "42P07", Message: "relation \"t\" already exists"}},
		{"CREATE SCHEMA s; CREATE VIEW s.v AS SELECT id FROM t", "ALTER VIEW v SET SCHEMA s", fixture.Diagnostic{Code: "42P07", Message: "relation \"v\" already exists in schema \"s\""}},
		{"", "ALTER VIEW absent RENAME TO z", fixture.Diagnostic{Code: "42P01", Message: "relation \"absent\" does not exist"}},
		{"", "DROP VIEW absent", fixture.Diagnostic{Code: "42P01", Message: "view \"absent\" does not exist"}},
		{"", "DROP VIEW absent_schema.v", fixture.Diagnostic{Code: "3F000", Message: "schema \"absent_schema\" does not exist"}},
		{"CREATE VIEW w AS SELECT id FROM v", "DROP VIEW v", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view v because other objects depend on it", Detail: "view w depends on view v", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}},
		{"", "ALTER VIEW v OWNER TO absent_role", fixture.Diagnostic{Code: "42704", Message: "role \"absent_role\" does not exist"}},
		{"", "ALTER VIEW v SET(security_invoker=wat)", fixture.Diagnostic{Code: "22023", Message: "invalid value for boolean option \"security_invoker\": wat"}},
		{"", "ALTER VIEW v SET(unknown_option=true)", fixture.Diagnostic{Code: "22023", Message: "unrecognized parameter \"unknown_option\""}},
		{"", "ALTER VIEW v SET(security_invoker=true,security_invoker=false)", fixture.Diagnostic{Code: "22023", Message: "parameter \"security_invoker\" specified more than once"}},
		{"", "ALTER VIEW v RESET(security_invoker=true)", fixture.Diagnostic{Code: "42601", Message: "RESET must not include values for parameters"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.SchemaReferenceCleanup(t, "s")
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,txt TEXT)", 0), fixture.Exec("INSERT INTO t VALUES(1,'a')", 1), fixture.Exec("CREATE VIEW v AS SELECT id,txt FROM t", 0)} {
				p.Step(t, s)
			}
			for _, query := range strings.Split(tc.setup, ";") {
				if query = strings.TrimSpace(query); query != "" {
					p.Step(t, fixture.Exec(query, 0))
				}
			}
			step := fixture.ImplicitError(tc.query, tc.want)

			t.Run("paired", func(t *testing.T) { p.Step(t, step) })
			p.Step(t, fixture.Select("SELECT * FROM v", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		})
	}
}

func TestReferenceViewReplacePrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0)} {
		p.Step(t, s)
	}
	for _, backend := range p.Backends() {
		db := backend.DB
		id, err := db.PrepareContext(p.Ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer id.Close()
		star, err := db.PrepareContext(p.Ctx, "SELECT * FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer star.Close()
		ddl, err := db.PrepareContext(p.Ctx, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer ddl.Close()
		initialRows, initialErr := id.QueryContext(p.Ctx)
		fixture.DeleteReferenceConsume(t, backend.PG, initialRows, initialErr, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		var before int64
		if err := db.QueryRowContext(p.Ctx, "SELECT id FROM v").Scan(&before); err != nil || before != 1 {
			t.Fatal(before, err)
		}
		for range 2 {
			r, err := ddl.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		}
		afterRows, afterErr := id.QueryContext(p.Ctx)
		fixture.DeleteReferenceConsume(t, backend.PG, afterRows, afterErr, fixture.Select("", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		var after int64
		if err := id.QueryRowContext(p.Ctx).Scan(&after); err != nil || after != 11 {
			t.Fatal(after, err)
		}
		rows, err := star.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("changed prepared shape returned rows")
		}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
		fixture.AssertDiagnostic(t, err, &want)
	}
	for _, q := range []string{"ALTER VIEW v RENAME COLUMN id TO key", "ALTER VIEW v RENAME TO renamed", "ALTER VIEW renamed SET(security_barrier=true)", "ALTER VIEW renamed RESET(security_barrier)", "DROP VIEW renamed"} {
		p.Step(t, fixture.Prepared(fixture.Exec(q, 0)))
	}
}

func TestReferenceViewReplaceAlterDrop(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA s", 0), fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0), fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0), fixture.Exec("ALTER VIEW v RENAME COLUMN id TO key", 0), fixture.Exec("ALTER VIEW v RENAME TO renamed", 0), fixture.Exec("ALTER VIEW renamed SET SCHEMA s", 0), fixture.Exec("ALTER VIEW s.renamed OWNER TO CURRENT_USER", 0), fixture.Exec("CREATE VIEW v AS SELECT 99 AS id FROM t", 0), fixture.Select("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}), fixture.ImplicitError("DROP VIEW s.renamed", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view s.renamed because other objects depend on it", Detail: "view w depends on view s.renamed", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}), fixture.ImplicitError("DROP VIEW v,w,missing", fixture.Diagnostic{Code: "42P01", Message: "view \"missing\" does not exist"}), fixture.Select("SELECT id FROM w", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}), fixture.Exec("DROP VIEW s.renamed,w RESTRICT", 0), fixture.Exec("DROP VIEW IF EXISTS absent,v", 0), fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}),
		fixture.Exec("CREATE VIEW a AS SELECT id FROM t", 0), fixture.Exec("CREATE VIEW b AS SELECT id FROM a", 0), fixture.Exec("CREATE VIEW c AS SELECT id FROM b", 0), fixture.Exec("DROP VIEW a CASCADE", 0), fixture.Select("SELECT count(*) FROM pg_class WHERE relname='a' OR relname='b' OR relname='c'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
	} {
		p.Step(t, s)
	}
}

func TestReferenceViewReplaceCycles(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id FROM v", 0), fixture.ImplicitError("SELECT id FROM v", fixture.Diagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), fixture.Prepared(fixture.ImplicitError("SELECT id FROM v", fixture.Diagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""})), fixture.ImplicitError("SELECT id FROM v FOR UPDATE", fixture.Diagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id FROM t", 0), fixture.Exec("CREATE VIEW w AS SELECT id FROM v", 0), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id FROM w", 0), fixture.ImplicitError("SELECT id FROM v", fixture.Diagnostic{Code: "42P17", Message: "infinite recursion detected in rules for relation \"v\""}), fixture.Exec("DROP VIEW v,w RESTRICT", 0), fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})} {
		p.Step(t, s)
	}
}
