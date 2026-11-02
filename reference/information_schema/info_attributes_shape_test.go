package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoAttributesShape(t *testing.T) {
	attrScenario(t, func(c *attrCase) {
		t := c.t
		var attributes [][]any
		for i, name := range attrColumns {
			domain, base, collation := "sql_identifier", int64(19), int64(950)
			if attrTypes[i] == "VARCHAR" {
				domain, base = "character_data", 1043
			}
			if i == 6 || i == 30 {
				domain, base = "yes_or_no", 1043
			}
			if attrTypes[i] == "INT4" {
				domain, base, collation = "cardinal_number", 23, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, c.p, c.b, c.c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.attributes'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		c.run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.attributes'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		c.exec("CREATE SCHEMA attr_scope", "CREATE TYPE attr_scope.pair AS (id integer)", "CREATE TYPE attr_scope.empty AS ()")
		want := [][]any{c.integer("pair", "id", 1)}
		for _, predicate := range []struct {
			where string
			rows  [][]any
			args  []any
		}{
			{"udt_schema='attr_scope' AND udt_name='pair'", want, nil},
			{"udt_schema='attr_scope' AND udt_name='empty'", nil, nil},
			{"udt_schema='absent'", nil, nil},
			{"udt_schema='attr_scope' AND udt_name='absent'", nil, nil},
			{"udt_schema=''", nil, nil}, {"udt_name=''", nil, nil},
			{"FALSE", nil, nil}, {"udt_name IS NULL", nil, nil},
			{"udt_schema=$1 AND udt_name=$2", nil, []any{nil, "pair"}},
			{"udt_schema=$1 AND udt_name=$2", nil, []any{"attr_scope", nil}},
		} {
			for _, star := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := attrProjection(c.b.PG, predicate.where, predicate.rows)
					s.Args, s.Prepared = predicate.args, prepared
					if star {
						s.Query = strings.Replace(s.Query, strings.Join(attrColumns, ","), "*", 1)
					}
					c.run(s)
					var rows *sql.Rows
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = c.c.PrepareContext(c.p.Ctx, s.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(c.p.Ctx, s.Args...)
					} else {
						rows, err = c.c.QueryContext(c.p.Ctx, s.Query, s.Args...)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					attrMetadata(t, types, c.b.PG, attrColumns, attrTypes)
					count := 0
					for rows.Next() {
						count++
					}
					if rows.Err() != nil || count != len(predicate.rows) {
						t.Fatalf("rows=%d err=%v", count, rows.Err())
					}
					attrMetadata(t, types, c.b.PG, attrColumns, attrTypes)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					attrMetadata(t, types, c.b.PG, attrColumns, attrTypes)
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
		where := "udt_schema=$1 AND udt_name=$2"
		h := c.hold(where)
		for _, args := range [][]any{{"attr_scope", "pair"}, {"attr_scope", "absent"}, {"attr_scope", nil}, {nil, "pair"}, {"", "pair"}, {"attr_scope", ""}, {"attr_scope", "pair"}} {
			var rows [][]any
			if args[0] == "attr_scope" && args[1] == "pair" {
				rows = want
			}
			c.held(h, where, args, rows...)
		}
		for _, prepared := range []bool{false, true} {
			names := []string{"attribute_default", "interval_precision", "maximum_cardinality"}
			bases := []string{"VARCHAR", "INT4", "INT4"}
			s := fixture.Select("SELECT attribute_default,interval_precision,maximum_cardinality FROM information_schema.attributes WHERE udt_schema='attr_scope'", names, bases, [][]any{{nil, nil, nil}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				attrMetadata(t, types, c.b.PG, names, bases)
			}
			c.run(s)
			var executor fixture.Executor = c.c
			if prepared {
				executor = dcHold(t, c.p, c.c, s.Query)
			}
			rows, err := executor.QueryContext(c.p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			attrMetadata(t, types, c.b.PG, names, bases)
			count := 0
			for rows.Next() {
				var defaultValue, intervalPrecision, maximumCardinality any
				if err := rows.Scan(&defaultValue, &intervalPrecision, &maximumCardinality); err != nil {
					t.Fatal(err)
				}
				if defaultValue != nil || intervalPrecision != nil || maximumCardinality != nil {
					t.Fatal("all-NULL projection populated a value")
				}
				count++
			}
			if rows.Err() != nil || count != 1 {
				t.Fatal("all-NULL rows", count, rows.Err())
			}
			attrMetadata(t, types, c.b.PG, names, bases)
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			attrMetadata(t, types, c.b.PG, names, bases)
		}
	})
}
