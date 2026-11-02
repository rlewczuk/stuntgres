package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnUdtUsageShape(t *testing.T) {
	cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cuIdentity(t, p, b, c, r)
		cuSeed(run)
		run(fixture.Exec("CREATE TABLE cu_scope.empty()", 0))
		predicates := []string{"table_schema='cu_scope'", "table_schema='cu_absent'", "table_schema='cu_scope' AND udt_name='absent'", "table_schema='cu_scope' AND table_name='absent'", "table_schema='cu_scope' AND table_name='empty'", "FALSE", "TRUE LIMIT 0"}
		for _, column := range cuColumns {
			predicates = append(predicates, "table_schema='cu_scope' AND "+column+" IS NULL")
		}
		for _, where := range predicates {
			var want [][]any
			if where == "table_schema='cu_scope'" {
				want = cuSeedRows(database)
			}
			for _, star := range []bool{false, true} {
				s := cuProjection(b.PG, where, want)
				if star {
					s.Query = strings.Replace(s.Query, strings.Join(cuColumns, ","), "*", 1)
				}
				cuCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
				for _, prepared := range []bool{false, true} {
					rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					cuMetadata(t, columns, b.PG)
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
					cuMetadata(t, columns, b.PG)
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
					cuMetadata(t, columns, b.PG)
				}
			}
		}
		// NULL extension is not a privilege-masking row: it is a real outer join.
		run(fixture.Exec("CREATE TABLE cu_scope.seed(v integer)", 0))
		run(fixture.Exec("INSERT INTO cu_scope.seed VALUES(1)", 1))
		null := cuProjection(b.PG, "FALSE", [][]any{{nil, nil, nil, nil, nil, nil, nil}})
		null.Query = "SELECT u.* FROM cu_scope.seed s LEFT JOIN information_schema.column_udt_usage u ON FALSE"
		cuCheck(t, p, c, r, cduHold(t, p, c, null.Query), null)
		for _, q := range []string{`CREATE TYPE cu_scope."Énum" AS ENUM('x')`, `CREATE TABLE cu_scope."MiXeD表"("Cöl" cu_scope."Énum")`} {
			run(fixture.Exec(q, 0))
		}
		s := cuProjection(b.PG, "table_schema='cu_scope' AND table_name=$1 AND column_name=$2", nil)
		held := cduHold(t, p, c, s.Query)
		for _, args := range [][]any{{"MiXeD表", "Cöl"}, {"mixed表", "Cöl"}, {"MiXeD表", "cöl"}, {nil, "Cöl"}} {
			step := s
			step.Args = args
			if args[0] == "MiXeD表" && args[1] == "Cöl" {
				step = cuProjection(b.PG, "table_schema='cu_scope' AND table_name=$1 AND column_name=$2", [][]any{cuRow(database, "cu_scope", "Énum", "cu_scope", "MiXeD表", "Cöl")})
				step.Args = args
			}
			cuCheck(t, p, c, r, held, step)
		}
		run(fixture.Exec(`DROP TABLE cu_scope."MiXeD表"`, 0))
		s = cuProjection(b.PG, "table_schema='cu_scope' AND table_name=$1 AND column_name=$2", nil)
		s.Args = []any{"MiXeD表", "Cöl"}
		cuCheck(t, p, c, r, held, s)
	})
}

func TestReferenceInfoColumnUdtUsageBuiltins(t *testing.T) {
	cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		s := cuProjection(b.PG, "table_schema='information_schema' AND table_name='column_udt_usage'", cuSelfRows(database))
		cuCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
		want := [][]any{cuRow(database, "pg_catalog", "oid", "pg_catalog", "pg_namespace", "oid"), cuRow(database, "pg_catalog", "name", "pg_catalog", "pg_namespace", "nspname"), cuRow(database, "pg_catalog", "oid", "pg_catalog", "pg_namespace", "nspowner"), cuRow(database, "pg_catalog", "_aclitem", "pg_catalog", "pg_namespace", "nspacl")}
		s = cuProjection(b.PG, "table_schema='pg_catalog' AND table_name='pg_namespace'", want)
		cuCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
		run(fixture.Select("SELECT schemaname,tablename,rulename FROM pg_catalog.pg_rules WHERE schemaname='information_schema' AND tablename='column_udt_usage'", []string{"schemaname", "tablename", "rulename"}, []string{"NAME", "NAME", "NAME"}, nil))
		run(fixture.Exec("CREATE ROLE cu_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION cu_reader", 0))
		run(cuProjection(b.PG, "(table_schema='information_schema' AND table_name='column_udt_usage') OR (table_schema='pg_catalog' AND table_name='pg_namespace')", nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
