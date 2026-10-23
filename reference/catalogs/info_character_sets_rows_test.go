package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCharacterSetsRows(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		for _, q := range []string{csQuery, "SELECT " + strings.Join(csColumns, ",") + " FROM information_schema.character_sets"} {
			held := c.hold(q)
			s := c.projection(q, c.rows("pg_catalog", "C"))
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
			c.run(held, s)
		}
		for _, predicate := range []string{"character_set_catalog IS NULL", "character_set_catalog IS NOT NULL", "FALSE"} {
			var want [][]any
			if predicate == "character_set_catalog IS NULL" {
				want = c.rows("pg_catalog", "C")
			}
			s := c.projection(csQuery+" WHERE "+predicate, want)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(c.hold(s.Query), s)
		}
		q := csQuery + " WHERE character_set_name=$1"
		held := c.hold(q)
		for _, arg := range []any{"UTF8", "LATIN1", nil, "UTF8"} {
			var want [][]any
			if arg == "UTF8" {
				want = c.rows("pg_catalog", "C")
			}
			s := c.projection(q, want, arg)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
	})
}

func TestReferenceInfoCharacterSetsMetadata(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		for _, empty := range []bool{false, true} {
			q := csQuery
			want := c.rows("pg_catalog", "C")
			if empty {
				q += " WHERE FALSE"
				want = nil
			}
			held := c.hold(q)
			for _, route := range []string{"direct", "prepared", "held"} {
				c.t.Run(route+"/"+q, func(t *testing.T) {
					var rows *sql.Rows
					var closeQuery func() error
					var err error
					if route == "held" {
						rows, err = held.QueryContext(c.p.Ctx, q)
						if rows != nil {
							closeQuery = rows.Close
						}
					} else {
						rows, closeQuery, err = fixture.TransactionQuery(c.p.Ctx, c.conn, q, route == "prepared")
					}
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					csMetadata(t, columns, c.b.PG)
					step := c.projection(q, want)
					count := 0
					for rows.Next() {
						if count >= len(want) {
							t.Fatal("unexpected extra row")
						}
						values, dest := make([]any, 8), make([]any, 8)
						for i := range values {
							dest[i] = &values[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, value := range values {
							step.CheckRaw(t, i, count, value)
						}
						count++
					}
					if rows.Err() != nil || count != len(want) {
						t.Fatalf("count=%d want=%d error=%v", count, len(want), rows.Err())
					}
					csMetadata(t, columns, c.b.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					csMetadata(t, columns, c.b.PG)
				})
			}
		}
	})
}

func TestReferenceInfoCharacterSetsIdentity(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		var attributes [][]any
		for i, name := range csColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		s := fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.character_sets'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes)
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, s)
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Prepared(s))
		s = fixture.Select(`SELECT c.relkind,t.typname,t.typrelid=c.oid AS same_relation,c.reltype=t.oid AS same_type,a.typelem=t.oid AS same_element,a.typnamespace=t.typnamespace AS same_namespace,n.nspname,a.typname AS array_name FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE c.oid='information_schema.character_sets'::regclass`, []string{"relkind", "typname", "same_relation", "same_type", "same_element", "same_namespace", "nspname", "array_name"}, []string{"CHAR", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "NAME"}, [][]any{{"v", "character_sets", true, true, true, true, "information_schema", "_character_sets"}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
