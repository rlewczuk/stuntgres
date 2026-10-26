package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsageShape(t *testing.T) {
	cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cduIdentity(t, p, b, c, r)
		cduSeed(run)
		for _, where := range []string{"table_schema='cdu'", "table_schema='cdu_absent'", "table_schema='cdu' AND domain_name='absent'", "table_schema='cdu' AND table_name='absent'", "FALSE", "domain_name IS NULL"} {
			var want [][]any
			if where == "table_schema='cdu'" {
				want = cduSeedRows(database)
			}
			for _, star := range []bool{false, true} {
				s := cduProjection(b.PG, where, want)
				if star {
					s.Query = strings.Replace(s.Query, strings.Join(cduColumns, ","), "*", 1)
				}
				held := cduHold(t, p, c, s.Query)
				cduCheck(t, p, c, r, held, s)
				for _, prepared := range []bool{false, true} {
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					cduMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						var cells [7]any
						if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6]); err != nil {
							t.Fatal(err)
						}
						for i, cell := range cells {
							s.CheckRaw(t, i, count, cell)
						}
						count++
					}
					if rows.Err() != nil || count != len(want) {
						t.Fatalf("count=%d want=%d error=%v", count, len(want), rows.Err())
					}
					cduMetadata(t, columns, b.PG)
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
					cduMetadata(t, columns, b.PG)
				}
			}
		}
		for _, star := range []bool{false, true} {
			s := cduProjection(b.PG, "table_schema='cdu' AND domain_name=$1", nil)
			if star {
				s.Query = strings.Replace(s.Query, strings.Join(cduColumns, ","), "*", 1)
			}
			held := cduHold(t, p, c, s.Query)
			for _, value := range []any{"parent", "absent", nil} {
				step := s
				step.Args = []any{value}
				if value == "parent" {
					step = cduProjection(b.PG, "table_schema='cdu' AND domain_name=$1", [][]any{cduSeedRows(database)[0], cduSeedRows(database)[2]})
					step.Query = s.Query
					step.Args = []any{value}
				}
				cduCheck(t, p, c, r, held, step)
				if value == nil {
					for _, x := range []fixture.Executor{c, held} {
						rows, err := x.QueryContext(p.Ctx, s.Query, nil)
						if err != nil {
							t.Fatal(err)
						}
						columns, err := rows.ColumnTypes()
						if err != nil {
							rows.Close()
							t.Fatal(err)
						}
						cduMetadata(t, columns, b.PG)
						if rows.Next() || rows.Err() != nil {
							rows.Close()
							t.Fatalf("NULL filter returned rows or error: %v", rows.Err())
						}
						cduMetadata(t, columns, b.PG)
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						cduMetadata(t, columns, b.PG)
					}
				}
			}
		}
	})
}

func TestReferenceInfoColumnDomainUsageBuiltins(t *testing.T) {
	cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		s := cduProjection(b.PG, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(database))
		cduCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
		var want [][]any
		for _, name := range []string{"constraint_catalog", "constraint_schema", "constraint_name", "domain_catalog", "domain_schema", "domain_name"} {
			want = append(want, cduRow(database, "information_schema", "sql_identifier", "information_schema", "domain_constraints", name))
		}
		for _, name := range []string{"is_deferrable", "initially_deferred"} {
			want = append(want, cduRow(database, "information_schema", "yes_or_no", "information_schema", "domain_constraints", name))
		}
		run(cduProjection(b.PG, "table_schema='information_schema' AND table_name='domain_constraints'", want))
		run(fixture.Exec("CREATE ROLE cdu_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION cdu_reader", 0))
		run(cduProjection(b.PG, "table_schema='information_schema' AND table_name IN ('column_domain_usage','domain_constraints')", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
