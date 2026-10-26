package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserDefinedTypesShape(t *testing.T) {
	udtScenario(t, func(c *udtCase) {
		t := c.t
		var attributes [][]any
		for i, name := range udtColumns {
			domain, base, collation := "sql_identifier", int64(19), int64(950)
			if udtTypes[i] == "VARCHAR" {
				domain, base = "character_data", 1043
			}
			if i == 4 || i == 5 {
				domain, base = "yes_or_no", 1043
			}
			if udtTypes[i] == "INT4" {
				domain, base, collation = "cardinal_number", 23, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, c.p, c.b, c.c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.user_defined_types'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		c.run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.user_defined_types'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		c.run(fixture.Select(`SELECT typname,typtypmod,typcollation::bigint FROM pg_catalog.pg_type WHERE typnamespace='information_schema'::regnamespace AND typname IN ('sql_identifier','character_data','yes_or_no','cardinal_number') ORDER BY typname`, []string{"typname", "typtypmod", "typcollation"}, []string{"NAME", "INT4", "INT8"}, [][]any{{"cardinal_number", int64(-1), int64(0)}, {"character_data", int64(-1), int64(950)}, {"sql_identifier", int64(-1), int64(950)}, {"yes_or_no", int64(7), int64(950)}}))
		c.exec("CREATE SCHEMA udt_scope", "CREATE TYPE udt_scope.i AS (v integer)", "CREATE TYPE udt_scope.v AS ()")
		want := [][]any{udtRow(c.database, "udt_scope", "i"), udtRow(c.database, "udt_scope", "v")}
		for _, predicate := range []struct {
			where string
			rows  [][]any
			args  []any
		}{
			{"user_defined_type_schema='udt_scope'", want, nil},
			{"user_defined_type_schema='udt_absent'", nil, nil},
			{"user_defined_type_schema='udt_scope' AND user_defined_type_name='absent'", nil, nil},
			{"user_defined_type_schema=''", nil, nil}, {"user_defined_type_name=''", nil, nil},
			{"FALSE", nil, nil}, {"user_defined_type_name IS NULL", nil, nil},
			{"user_defined_type_schema='udt_scope' AND user_defined_type_name=$1", nil, []any{nil}},
		} {
			for _, star := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := udtProjection(c.b.PG, predicate.where, predicate.rows)
					s.Args, s.Prepared = predicate.args, prepared
					if star {
						s.Query = strings.Replace(s.Query, strings.Join(udtColumns, ","), "*", 1)
					}
					c.run(s)
					// Retain the actual descriptor objects through EOF and Close.
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
					udtMetadata(t, types, c.b.PG)
					count := 0
					for rows.Next() {
						count++
					}
					if rows.Err() != nil || count != len(predicate.rows) {
						t.Fatalf("rows=%d err=%v", count, rows.Err())
					}
					udtMetadata(t, types, c.b.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					udtMetadata(t, types, c.b.PG)
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
		where := "user_defined_type_schema=$1 AND user_defined_type_name=$2"
		h := c.hold(where)
		for _, args := range [][]any{{"udt_scope", "i"}, {"udt_scope", "absent"}, {"udt_scope", nil}, {"absent", "i"}, {nil, "i"}, {"", "i"}, {"udt_scope", ""}, {"udt_scope", "i"}} {
			var rows [][]any
			if args[0] == "udt_scope" && args[1] == "i" {
				rows = want[:1]
			}
			c.held(h, where, args, rows...)
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT is_final,numeric_precision FROM information_schema.user_defined_types WHERE user_defined_type_schema='udt_scope'", []string{"is_final", "numeric_precision"}, []string{"VARCHAR", "INT4"}, [][]any{{nil, nil}, {nil, nil}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(types) != 2 {
					t.Fatal("null-only width", len(types))
				}
				if length, known := types[0].Length(); length != 3 || !known {
					t.Fatal("null yes_or_no length", length, known)
				}
				for _, typ := range types {
					n, k := typ.Nullable()
					if n != !c.b.PG || k != !c.b.PG {
						t.Fatal("null-only nullability", n, k)
					}
				}
			}
			c.run(s)
		}
	})
}
