package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTransformsShape(t *testing.T) {
	trScenario(t, func(c *trCase) {
		c.routes(trQuery, nil)
		c.routes("SELECT "+trProjection+" FROM information_schema.transforms", nil)
		for _, predicate := range []string{"false", "NULL::boolean", "udt_name='absent'", "transform_type IS NULL"} {
			c.routes(trQuery+" WHERE "+predicate, nil)
		}
		for _, value := range []any{"absent", nil} {
			c.routes(trQuery+" WHERE udt_name=$1", nil, value)
		}
		c.routes(trNullQuery, trNullRow)
		c.inventory(c.conn)
		c.exec("SET search_path=information_schema,pg_catalog")
		c.routes("SELECT * FROM transforms", nil)
		c.exec("RESET search_path")
	})
}

func TestReferenceInfoTransformsMetadata(t *testing.T) {
	trScenario(t, func(c *trCase) {
		var self, logical [][]any
		for i, name := range trColumns {
			domain, base, baseID := "sql_identifier", "name", int64(19)
			if i == 7 {
				domain, base, baseID = "character_data", "character varying", 1043
			}
			self = append(self, []any{name, int64(i + 1), "YES", c.database, "information_schema", domain, base})
			logical = append(logical, []any{int64(i + 1), name, domain, "d", baseID, int64(-1), false, int64(950), int64(0), false, "information_schema"})
		}
		s := fixture.Select("SELECT column_name,ordinal_position::bigint,is_nullable,domain_catalog,domain_schema,domain_name,data_type FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='transforms' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_catalog", "domain_schema", "domain_name", "data_type"}, []string{"NAME", "INT8", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR"}, self)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q := "SELECT a.attnum,a.attname,t.typname,t.typtype,t.typbasetype::bigint AS typbasetype,a.atttypmod,a.attnotnull,a.attcollation::bigint AS attcollation,a.attndims,t.typnotnull,n.nspname FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.transforms'::regclass AND a.attnum>0 ORDER BY a.attnum"
		s = fixture.Select(q, []string{"attnum", "attname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "nspname"}, []string{"INT2", "NAME", "NAME", "CHAR", "INT8", "INT4", "BOOL", "INT8", "INT2", "BOOL", "NAME"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = "SELECT c.relkind,t.typtype,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.transforms'::regclass"
		c.run(c.conn, fixture.Select(q, []string{"relkind", "typtype", "row_link", "element_link", "array_link"}, []string{"CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", "c", true, true, true}}))
		held := c.hold(trQuery)
		for _, q := range []string{trQuery, trNullQuery} {
			want := 0
			if q == trNullQuery {
				want = 1
			}
			rows, err := c.conn.QueryContext(c.p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			saved := c.consume(rows, want)
			c.run(held, c.step(trQuery, nil))
			trMetadata(t, saved, c.b.PG)
			prepared := c.hold(q)
			rows, err = prepared.Statement.QueryContext(c.p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(rows, want)
			if err := prepared.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceInfoTransformsInventory(t *testing.T) {
	trScenario(t, func(c *trCase) {
		held := c.hold(trQuery)
		check := func() { c.inventory(c.conn); c.run(held, c.step(trQuery, nil)) }
		check()
		c.exec("CREATE TYPE public.tr_enum AS ENUM ('actual')", "CREATE DOMAIN public.tr_domain AS integer", "CREATE FUNCTION public.tr_identity(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
		check()
		c.exec("CREATE EXTENSION unaccent VERSION '1.1'")
		check()
		c.exec("DROP EXTENSION unaccent", "DROP FUNCTION public.tr_identity(integer)", "DROP DOMAIN public.tr_domain", "DROP TYPE public.tr_enum")
		check()
		// No supported transform definitions exist; no populated lifecycle is claimed.
	})
}
