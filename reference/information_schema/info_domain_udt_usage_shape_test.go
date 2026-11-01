package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageShape(t *testing.T) {
	duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		duIdentity(t, p, b, c, r)
		duSeed(run)
		var discovered, self [][]any
		for i, name := range duColumns {
			discovered = append(discovered, []any{name, int64(i + 1), "YES", "information_schema", "sql_identifier", "pg_catalog", "name"})
			self = append(self, cduRow(database, "information_schema", "sql_identifier", "information_schema", "domain_udt_usage", name))
		}
		run(fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name,udt_schema,udt_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='domain_udt_usage' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name", "udt_schema", "udt_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME", "NAME", "NAME"}, discovered))
		run(cduProjection(b.PG, "table_schema='information_schema' AND table_name='domain_udt_usage'", self))
		run(fixture.Select("SELECT rulename FROM pg_catalog.pg_rules WHERE schemaname='information_schema' AND tablename='domain_udt_usage'", []string{"rulename"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE ROLE du_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION du_reader", 0))
		run(cduProjection(b.PG, "table_schema='information_schema' AND table_name='domain_udt_usage'", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, where := range []string{"domain_schema='du_scope'", "domain_schema='du_absent'", "domain_schema='du_scope' AND domain_name='absent'", "domain_schema='du_scope' AND udt_name='absent'", "FALSE", "domain_name IS NULL", "udt_catalog IS NULL", "udt_schema IS NULL", "udt_name IS NULL", "domain_catalog IS NULL", "domain_schema IS NULL"} {
			var want [][]any
			if where == "domain_schema='du_scope'" {
				want = duSeedRows(database)
			}
			for _, star := range []bool{false, true} {
				s := duProjection(b.PG, where, want)
				if star {
					s.Query = strings.Replace(s.Query, strings.Join(duColumns, ","), "*", 1)
				}
				held := cduHold(t, p, c, s.Query)
				duCheck(t, p, c, r, held, s)
				for _, prepared := range []bool{false, true} {
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					duMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						var cells [6]any
						if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5]); err != nil {
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
					duMetadata(t, columns, b.PG)
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
					duMetadata(t, columns, b.PG)
				}
			}
		}
		for _, star := range []bool{false, true} {
			s := duProjection(b.PG, "domain_schema='du_scope' AND domain_name=$1", nil)
			if star {
				s.Query = strings.Replace(s.Query, strings.Join(duColumns, ","), "*", 1)
			}
			held := cduHold(t, p, c, s.Query)
			for _, value := range []any{"p", "absent", nil} {
				step := s
				step.Args = []any{value}
				if value == "p" {
					step = duProjection(b.PG, "domain_schema='du_scope' AND domain_name=$1", [][]any{duSeedRows(database)[0]})
					step.Query = s.Query
					step.Args = []any{value}
				}
				duCheck(t, p, c, r, held, step)
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
						duMetadata(t, columns, b.PG)
						if rows.Next() || rows.Err() != nil {
							rows.Close()
							t.Fatalf("NULL filter returned rows or error: %v", rows.Err())
						}
						duMetadata(t, columns, b.PG)
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						duMetadata(t, columns, b.PG)
					}
				}
			}
		}
	})
}
