package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserMappingOptionsShape(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		c.check(nil)
		c.exec("CREATE SERVER umo_server FOREIGN DATA WRAPPER umo_fdw")
		c.check(nil)
		c.exec("CREATE USER MAPPING FOR umo_empty SERVER umo_server")
		c.check(nil)
		c.exec("DROP SERVER umo_server CASCADE")
		c.seed()
		want := c.rows("all")
		c.check(want)
		c.routes("SELECT * FROM information_schema.user_mapping_options WHERE foreign_server_name=$1 ORDER BY authorization_identifier,option_name", want, "umo_server")
		star := c.selectStep("SELECT * FROM information_schema.user_mapping_options WHERE foreign_server_name=$1 ORDER BY authorization_identifier,option_name", want, "umo_server")
		starHeld := iumHeld(t, c.p, c.conn, c.recorder, star.Query)
		starHeld(star)
		stmt, held := c.hold()
		for _, server := range []any{"umo_server", "absent", nil} {
			expected := want
			if server != "umo_server" {
				expected = nil
			}
			s := c.selectStep(umoQuery, expected, server)
			c.run(s)
			c.run(fixture.Prepared(s))
			held(s)
			for _, prepared := range []bool{false, true} {
				literal := "NULL"
				if server != nil {
					literal = "'" + server.(string) + "'"
				}
				query := strings.ReplaceAll(umoQuery, "$1", literal)
				rows, closeQuery, err := fixture.TransactionQuery(c.p.Ctx, c.conn, query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				c.consume(rows, expected)
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := stmt.QueryContext(c.p.Ctx, server)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(rows, expected)
		}
		c.routes("SELECT "+umoProjection+" FROM information_schema.user_mapping_options WHERE false", nil)
		c.exec("CREATE FOREIGN TABLE umo_decoy(a text OPTIONS(column_only 'decoy')) SERVER umo_server OPTIONS(table_only 'decoy')")
		c.check(want)
		// The decoy witnesses mapping-option exclusion, not foreign-table
		// update capabilities during the independent columns introspection.
		c.exec("DROP FOREIGN TABLE umo_decoy")
		c.routes("SELECT "+umoProjection+" FROM information_schema.user_mapping_options WHERE foreign_server_name='umo_server' AND option_value=''", [][]any{c.row("umo_alice", "umo_server", "empty", "")})
		long := strings.Repeat("x", 80) + " é='quoted' \\ tail  "
		c.exec("CREATE USER MAPPING FOR umo_denied SERVER umo_server OPTIONS(long '" + strings.ReplaceAll(long, "'", "''") + "')")
		c.routes("SELECT "+umoProjection+" FROM information_schema.user_mapping_options WHERE foreign_server_name='umo_server' AND option_name='long'", [][]any{c.row("umo_denied", "umo_server", "long", long)})
		c.exec("DROP USER MAPPING FOR umo_denied SERVER umo_server", "GRANT USAGE ON FOREIGN SERVER umo_server TO umo_reader", "SET SESSION AUTHORIZATION umo_reader")
		c.routes("SELECT "+umoProjection+" FROM information_schema.user_mapping_options WHERE foreign_server_name='umo_server' AND option_value IS NULL ORDER BY authorization_identifier,option_name", c.rows("none"))
		c.routes("SELECT "+umoProjection+" FROM information_schema.user_mapping_options WHERE foreign_server_name='umo_server' AND option_value=''", nil)
		rows, err := c.conn.QueryContext(c.p.Ctx, umoQuery, "umo_server")
		if err != nil {
			t.Fatal(err)
		}
		c.consume(rows, c.rows("none"))
		c.exec("RESET SESSION AUTHORIZATION")
		var self, attributes [][]any
		for i, name := range umoColumns {
			domain, base, oid := "sql_identifier", "name", int64(19)
			if i == 4 {
				domain, base, oid = "character_data", "character varying", 1043
			}
			self = append(self, []any{name, int64(i + 1), "YES", c.database, "information_schema", domain, base})
			attributes = append(attributes, []any{int64(i + 1), name, domain, "d", oid, int64(-1), false, int64(950), int64(0), false, "information_schema"})
		}
		s := fixture.Select("SELECT column_name,ordinal_position::bigint,is_nullable,domain_catalog,domain_schema,domain_name,data_type FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='user_mapping_options' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_catalog", "domain_schema", "domain_name", "data_type"}, []string{"NAME", "INT8", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR"}, self)
		c.run(s)
		c.run(fixture.Prepared(s))
		s = fixture.Select("SELECT a.attnum,a.attname,t.typname,t.typtype,t.typbasetype::bigint AS typbasetype,a.atttypmod,a.attnotnull,a.attcollation::bigint AS attcollation,a.attndims,t.typnotnull,n.nspname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.user_mapping_options'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attnum", "attname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "nspname"}, []string{"INT2", "NAME", "NAME", "CHAR", "INT8", "INT4", "BOOL", "INT8", "INT2", "BOOL", "NAME"}, attributes)
		c.run(s)
		c.run(fixture.Prepared(s))
		c.run(fixture.Select("SELECT c.relkind,t.typtype,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.user_mapping_options'::regclass", []string{"relkind", "typtype", "row_link", "element_link", "array_link"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", "c", true, true, true}}))
	})
}
