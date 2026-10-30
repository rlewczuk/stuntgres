package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignServersShape(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		selfQuery := "SELECT column_name,ordinal_position::bigint,is_nullable,domain_catalog,domain_schema,domain_name,data_type FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='foreign_servers' ORDER BY ordinal_position"
		var self [][]any
		for i, name := range fsvColumns {
			domain, base := "sql_identifier", "name"
			if i == 4 || i == 5 {
				domain, base = "character_data", "character varying"
			}
			self = append(self, []any{name, int64(i + 1), "YES", c.database, "information_schema", domain, base})
		}
		c.run(c.conn, fixture.Select(selfQuery, []string{"column_name", "ordinal_position", "is_nullable", "domain_catalog", "domain_schema", "domain_name", "data_type"}, []string{"NAME", "INT8", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR"}, self))
		c.routes(fsvQuery, nil, "fsv_text")
		c.routes("SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name", nil)
		c.wrapper()
		c.routes("SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name", nil)
		c.exec("CREATE SERVER fsv_null FOREIGN DATA WRAPPER fsv_wrapper", "CREATE SERVER fsv_empty TYPE '' VERSION '' FOREIGN DATA WRAPPER fsv_wrapper", "CREATE SERVER fsv_text TYPE 'é=🙂' VERSION 'v 1' FOREIGN DATA WRAPPER fsv_wrapper OPTIONS(password 'secret',empty '')")
		want := [][]any{c.row("fsv_text", "fsv_wrapper", c.owner, "é=🙂", "v 1")}
		inventory := [][]any{c.row("fsv_empty", "fsv_wrapper", c.owner, "", ""), c.row("fsv_null", "fsv_wrapper", c.owner, nil, nil), want[0]}
		c.routes("SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name", inventory)
		c.exec("CREATE SCHEMA fsv_scope", "CREATE FOREIGN TABLE fsv_scope.options(id integer OPTIONS(column_secret 'column-only')) SERVER fsv_text OPTIONS(password 'table-secret')", "CREATE USER MAPPING FOR PUBLIC SERVER fsv_text OPTIONS(password 'mapping-secret')", "CREATE USER MAPPING FOR CURRENT_USER SERVER fsv_text OPTIONS(password 'named-secret')")
		c.routes("SELECT * FROM information_schema.foreign_servers ORDER BY foreign_server_name", inventory)
		c.routes(fsvQuery, want, "fsv_text")
		c.routes("SELECT * FROM information_schema.foreign_servers WHERE foreign_server_name=$1 ORDER BY foreign_server_name", want, "fsv_text")
		reordered := fixture.Select("SELECT authorization_identifier,foreign_server_version,foreign_server_name FROM information_schema.foreign_servers WHERE foreign_server_name=$1", []string{"authorization_identifier", "foreign_server_version", "foreign_server_name"}, []string{"NAME", "VARCHAR", "NAME"}, [][]any{{c.owner, "v 1", "fsv_text"}}, "fsv_text")
		c.run(c.conn, reordered)
		c.run(c.conn, fixture.Prepared(reordered))
		for _, predicate := range []string{"false", "NULL::boolean", "foreign_server_name='absent'"} {
			c.routes("SELECT "+fsvProjection+" FROM information_schema.foreign_servers WHERE "+predicate, nil)
		}
		held := c.hold(c.conn, fsvQuery)
		for _, server := range []any{"fsv_text", "absent", nil} {
			rows := want
			if server != "fsv_text" {
				rows = nil
			}
			c.check(held, server, rows)
			r, err := c.conn.QueryContext(c.p.Ctx, fsvQuery, server)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(r, rows)
			prepared, err := c.conn.PrepareContext(c.p.Ctx, fsvQuery)
			if err != nil {
				t.Fatal(err)
			}
			r, err = prepared.QueryContext(c.p.Ctx, server)
			if err != nil {
				prepared.Close()
				t.Fatal(err)
			}
			c.consume(r, rows)
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
			r, err = held.Statement.QueryContext(c.p.Ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(r, rows)
		}
		q := "SELECT o.* FROM (SELECT 1 AS n) s LEFT JOIN information_schema.foreign_servers o ON o.foreign_server_name='fsv_text' AND false"
		c.routes(q, [][]any{{nil, nil, nil, nil, nil, nil, nil}})
		r, err := c.conn.QueryContext(c.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		c.consume(r, [][]any{{nil, nil, nil, nil, nil, nil, nil}})
		q = "SELECT a.attnum,a.attname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,n.nspname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.foreign_servers'::regclass AND a.attnum>0 ORDER BY a.attnum"
		var logical [][]any
		for i, name := range fsvColumns {
			domain, base := "sql_identifier", int64(19)
			if i == 4 || i == 5 {
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
		q = "SELECT c.relkind,t.typtype,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.foreign_servers'::regclass"
		c.run(c.conn, fixture.Select(q, []string{"relkind", "typtype", "row_link", "element_link", "array_link"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", "c", true, true, true}}))
		var definition string
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_catalog.pg_get_viewdef('information_schema.foreign_servers'::regclass)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		wantDefinition := "SELECT foreign_server_catalog, foreign_server_name, foreign_data_wrapper_catalog, foreign_data_wrapper_name, foreign_server_type, foreign_server_version, authorization_identifier FROM information_schema._pg_foreign_servers;"
		if strings.Join(strings.Fields(definition), " ") != wantDefinition {
			t.Fatalf("released view definition=%q want=%q", definition, wantDefinition)
		}
	})
}
