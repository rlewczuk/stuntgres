package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainsShape(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		t := c.t
		var attributes [][]any
		for i, name := range idmColumns {
			domain, base, collation := "sql_identifier", int64(19), int64(950)
			if idmTypes[i] == "VARCHAR" {
				domain, base = "character_data", 1043
			}
			if idmTypes[i] == "INT4" {
				domain, base, collation = "cardinal_number", 23, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, c.p, c.b, c.c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.domains'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		c.run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.domains'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		c.exec("CREATE SCHEMA idm_scope", "CREATE DOMAIN idm_scope.i AS integer", `CREATE DOMAIN idm_scope.v AS varchar(5) COLLATE "C" DEFAULT 'abc'`)
		want := [][]any{idmInteger(c.database, "idm_scope", "i", nil), idmVarchar(c.database, "idm_scope", "v", "'abc'::character varying")}
		for _, predicate := range []struct {
			where string
			rows  [][]any
			args  []any
		}{
			{"domain_schema='idm_scope'", want, nil},
			{"domain_schema='idm_absent'", nil, nil},
			{"domain_schema='idm_scope' AND domain_name='absent'", nil, nil},
			{"FALSE", nil, nil}, {"domain_name IS NULL", nil, nil},
			{"domain_schema='idm_scope' AND domain_name=$1", nil, []any{nil}},
		} {
			for _, star := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := idmProjection(c.b.PG, predicate.where, predicate.rows)
					s.Args, s.Prepared = predicate.args, prepared
					if star {
						s.Query = strings.Replace(s.Query, strings.Join(idmColumns, ","), "*", 1)
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
					idmMetadata(t, types, c.b.PG)
					count := 0
					for rows.Next() {
						count++
					}
					if rows.Err() != nil || count != len(predicate.rows) {
						t.Fatalf("rows=%d err=%v", count, rows.Err())
					}
					idmMetadata(t, types, c.b.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					idmMetadata(t, types, c.b.PG)
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
		where := "domain_schema='idm_scope' AND domain_name=$1"
		h := c.hold(where)
		for _, arg := range []any{"i", "absent", nil} {
			var rows [][]any
			if arg == "i" {
				rows = want[:1]
			}
			c.held(h, where, []any{arg}, rows...)
		}
	})
}
