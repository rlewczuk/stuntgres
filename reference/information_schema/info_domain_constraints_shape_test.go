package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainConstraintsShape(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		var attributes [][]any
		for i, name := range dcColumns {
			domain, base := "sql_identifier", int64(19)
			if i >= 6 {
				domain, base = "yes_or_no", 1043
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.domain_constraints'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.domain_constraints'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		dcSeed(run)
		for _, s := range []fixture.Step{
			dcProjection(b.PG, "domain_schema='dc_scope'", dcSeedRows(database)),
			dcProjection(b.PG, "domain_schema='dc_absent'", nil),
			dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name='absent'", nil),
			dcProjection(b.PG, "FALSE", nil),
			dcProjection(b.PG, "domain_name IS NULL", nil),
			dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name='plain'", nil),
		} {
			for _, prepared := range []bool{false, true} {
				for _, star := range []bool{false, true} {
					step := s
					step.Prepared = prepared
					if star {
						step.Query = strings.Replace(step.Query, strings.Join(dcColumns, ","), "*", 1)
					}
					run(step)
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, step.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					dcMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						var cells [8]any
						if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6], &cells[7]); err != nil {
							t.Fatal(err)
						}
						for i, cell := range cells {
							step.CheckRaw(t, i, count, cell)
						}
						count++
					}
					if err := rows.Err(); err != nil || count != len(step.Rows) {
						t.Fatalf("rows=%d error=%v", count, err)
					}
					dcMetadata(t, columns, b.PG)
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
					dcMetadata(t, columns, b.PG)
				}
			}
		}
		s := dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name=$1", nil)
		held := dcHold(t, p, c, s.Query)
		for _, value := range []any{"a", "absent", nil} {
			want := [][]any(nil)
			if value == "a" {
				want = [][]any{dcRow(database, "dc_scope", "same", "a")}
			}
			step := dcProjection(b.PG, "domain_schema='dc_scope' AND domain_name=$1", want)
			step.Args = []any{value}
			dcCheck(t, p, c, r, held, step)
		}
		// Parameterized NULL also retains its complete description with star
		// and explicit projection, before EOF and after closing the cursor.
		for _, star := range []bool{false, true} {
			step := s
			step.Args = []any{nil}
			if star {
				step.Query = strings.Replace(step.Query, strings.Join(dcColumns, ","), "*", 1)
			}
			for _, prepared := range []bool{false, true} {
				step.Prepared = prepared
				run(step)
				var rows *sql.Rows
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx, step.Args...)
				} else {
					rows, err = c.QueryContext(p.Ctx, step.Query, step.Args...)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				dcMetadata(t, columns, b.PG)
				if rows.Next() || rows.Err() != nil {
					t.Fatal("NULL predicate returned a row or error", rows.Err())
				}
				dcMetadata(t, columns, b.PG)
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				dcMetadata(t, columns, b.PG)
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	})
}
