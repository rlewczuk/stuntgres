package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnOptionsShape(t *testing.T) {
	copScenario(t, func(c *copCase) {
		selfQuery := "SELECT column_name,ordinal_position::bigint,is_nullable,domain_catalog,domain_schema,domain_name,data_type FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='column_options' ORDER BY ordinal_position"
		var self [][]any
		for i, name := range copColumns {
			domain, base := "sql_identifier", "name"
			if i == 5 {
				domain, base = "character_data", "character varying"
			}
			self = append(self, []any{name, int64(i + 1), "YES", c.database, "information_schema", domain, base})
		}
		c.run(c.conn, fixture.Select(selfQuery, []string{"column_name", "ordinal_position", "is_nullable", "domain_catalog", "domain_schema", "domain_name", "data_type"}, []string{"NAME", "INT8", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR"}, self))
		c.seed()
		want := c.seedRows()
		c.routes(copQuery, want, "cop_scope")
		c.routes("SELECT * FROM information_schema.column_options WHERE table_schema=$1 ORDER BY table_name,column_name,option_name", want, "cop_scope")
		for _, predicate := range []string{"false", "NULL::boolean", "table_name='absent'", "option_value IS NULL"} {
			c.routes("SELECT "+copProjection+" FROM information_schema.column_options WHERE table_schema='cop_scope' AND "+predicate, nil)
		}
		held := c.hold(c.conn, copQuery)
		for _, schema := range []any{"cop_scope", "absent", nil} {
			rows := want
			if schema != "cop_scope" {
				rows = nil
			}
			c.check(held, schema, rows)
			r, err := c.conn.QueryContext(c.p.Ctx, copQuery, schema)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(r, rows)
			prepared, err := c.conn.PrepareContext(c.p.Ctx, copQuery)
			if err != nil {
				t.Fatal(err)
			}
			r, err = prepared.QueryContext(c.p.Ctx, schema)
			if err != nil {
				prepared.Close()
				t.Fatal(err)
			}
			c.consume(r, rows)
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
			r, err = held.Statement.QueryContext(c.p.Ctx, schema)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(r, rows)
		}
		q := "SELECT o.* FROM (SELECT 1 AS n) s LEFT JOIN information_schema.column_options o ON o.table_schema='cop_scope' AND false"
		c.routes(q, [][]any{{nil, nil, nil, nil, nil, nil}})
		r, err := c.conn.QueryContext(c.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		c.consume(r, [][]any{{nil, nil, nil, nil, nil, nil}})
		q = "SELECT a.attnum,a.attname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,n.nspname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.column_options'::regclass AND a.attnum>0 ORDER BY a.attnum"
		var logical [][]any
		for i, name := range copColumns {
			domain, base := "sql_identifier", int64(19)
			if i == 5 {
				domain, base = "character_data", 1043
			}
			logical = append(logical, []any{int64(i + 1), name, domain, "d", base, int64(-1), false, int64(950), int64(0), false, "information_schema"})
		}
		s := fixture.Select(q, []string{"attnum", "attname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "nspname"}, []string{"INT2", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "NAME"}, logical)
		// Cast OID fields for a portable exact logical assertion.
		s.Query = strings.ReplaceAll(strings.ReplaceAll(q, "t.typbasetype,", "t.typbasetype::bigint AS typbasetype,"), "a.attcollation,", "a.attcollation::bigint AS attcollation,")
		s.Types[4], s.Types[7] = "INT8", "INT8"
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = "SELECT c.relkind,t.typtype,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.column_options'::regclass"
		c.run(c.conn, fixture.Select(q, []string{"relkind", "typtype", "row_link", "element_link", "array_link"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", "c", true, true, true}}))
	})
}

func TestReferenceInfoColumnOptionsPopulation(t *testing.T) {
	copScenario(t, func(c *copCase) {
		c.exec("CREATE SCHEMA cop_scope")
		c.routes(copQuery, nil, "cop_scope")
		c.exec("DROP SCHEMA cop_scope")
		c.seed()
		c.routes(copQuery, c.seedRows(), "cop_scope")
		c.exec("CREATE USER MAPPING FOR PUBLIC SERVER cop_server OPTIONS(password 'mapping-only')", "CREATE TABLE cop_scope.ordinary(id integer)", "CREATE FOREIGN TABLE cop_scope.unoptioned(id integer) SERVER cop_server OPTIONS(remote 'table-only')", "CREATE FOREIGN TABLE cop_scope.zero() SERVER cop_server")
		c.routes(copQuery, c.seedRows(), "cop_scope")
		c.routes("SELECT "+copProjection+" FROM information_schema.column_options WHERE table_schema='cop_scope' AND option_value=''", [][]any{c.row("options", "label", "empty", "")})
		long := strings.Repeat("x", 80) + " é='quoted' \\ tail  "
		c.exec("CREATE FOREIGN TABLE cop_scope.long_value(a text OPTIONS(remote '" + strings.ReplaceAll(long, "'", "''") + "')) SERVER cop_server")
		c.routes("SELECT "+copProjection+" FROM information_schema.column_options WHERE table_schema='cop_scope' AND table_name='long_value'", [][]any{c.row("long_value", "a", "remote", long)})
		c.exec("DROP FOREIGN TABLE cop_scope.options,cop_scope.unoptioned,cop_scope.zero,cop_scope.long_value")
		c.routes(copQuery, nil, "cop_scope")
	})
}
