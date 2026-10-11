package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedRebindDDL(t *testing.T) {
	preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE rb_diag(a INT)", 0))
		run(fixture.Exec("INSERT INTO rb_diag VALUES(7)", 1))
		explicit := preparedRebindRoutes(t, p, b, c, "SELECT a FROM rb_diag", "PREPARE rb_p AS SELECT a FROM rb_diag", "EXECUTE rb_p")
		star := preparedRebindRoutes(t, p, b, c, "SELECT * FROM rb_diag", "PREPARE rb_star AS SELECT * FROM rb_diag", "EXECUTE rb_star")
		read := func(routes []preparedRebindRoute, value int64) {
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"a"}, []string{"INT4"}, [][]any{{value}})
			}
		}
		read(explicit, 7)
		read(star, 7)
		run(fixture.Exec("ALTER TABLE rb_diag ADD unused INT", 0))
		read(explicit, 7)
		shape := sqlPreparedDiagnostic("0A000", "cached plan must not change result type")
		for _, r := range star {
			preparedRebindFailure(t, p, b, r, shape)
		}
		run(fixture.Exec("DROP TABLE rb_diag", 0))
		for i, r := range explicit {
			d := sqlPreparedDiagnostic("42P01", `relation "rb_diag" does not exist`)
			// The original SQL definition adds the 16-character "PREPARE rb_p AS " prefix.
			d.Position = []string{"15", "31"}[i]
			preparedRebindFailure(t, p, b, r, d)
		}
		run(fixture.Exec("CREATE TABLE rb_diag(b TEXT,unused INT)", 0))
		for i, r := range explicit {
			d := sqlPreparedDiagnostic("42703", `column "a" does not exist`)
			d.Position = []string{"8", "24"}[i]
			preparedRebindFailure(t, p, b, r, d)
		}
		run(fixture.Exec("DROP TABLE rb_diag", 0))
		run(fixture.Exec("CREATE TABLE rb_diag(a INT)", 0))
		run(fixture.Exec("INSERT INTO rb_diag VALUES(9)", 1))
		read(explicit, 9)
		read(star, 9) // Neither failed analysis replaced the original signature.
		run(fixture.Exec("ALTER TABLE rb_diag ADD unused INT", 0))
		for _, r := range star {
			preparedRebindFailure(t, p, b, r, shape)
		}
		run(fixture.Exec("ALTER TABLE rb_diag DROP unused", 0))
		read(star, 9)
	})
}

func TestReferencePreparedRebindMetadata(t *testing.T) {
	preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE rb_meta(v INT)", 0))
		routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM rb_meta", "PREPARE rb_meta_read AS SELECT v FROM rb_meta", "EXECUTE rb_meta_read")
		check := func(nullable bool) {
			for _, r := range routes {
				preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, nil)
				rows, err := r.QueryContext(p.Ctx, r.SQL)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 1 {
					rows.Close()
					t.Fatal(columns, err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, columns, []string{"v"}, []string{"INT4"}, []bool{nullable})
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("empty result changed", rows.Err())
				}
				rows.Close()
			}
		}
		check(true)
		run(fixture.Exec("ALTER TABLE rb_meta ALTER v SET NOT NULL", 0))
		check(false)
		run(fixture.Exec("ALTER TABLE rb_meta ALTER v SET DEFAULT 12", 0))
		check(false)
		run(fixture.Exec("ALTER TABLE rb_meta ALTER v DROP NOT NULL", 0))
		run(fixture.Exec("ALTER TABLE rb_meta ALTER v DROP DEFAULT", 0))
		check(true)
		// Output origin is not fixed: same-shaped replacement is allowed.
		run(fixture.Exec("DROP TABLE rb_meta", 0))
		run(fixture.Exec("CREATE TABLE rb_meta(v INT NOT NULL)", 0))
		check(false)
	})

	for _, routeName := range []string{"go", "sql"} {
		t.Run("writes/"+routeName, func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { sqlPreparedStep(t, p, b, c, s) }
				run(fixture.Exec("CREATE TABLE rb_write(v INT)", 0))
				run(fixture.Exec("INSERT INTO rb_write VALUES(7)", 1))
				query := "UPDATE rb_write SET v=8 RETURNING *"
				var returning preparedRebindRoute
				var insert preparedRebindRoute
				var args []any
				if routeName == "go" {
					returning = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
					query = "INSERT INTO rb_write(v) VALUES($1)"
					insert = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
					args = []any{12}
				} else {
					run(fixture.Exec("PREPARE rb_return AS "+query, 0))
					returning = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_return"}
					run(fixture.Exec("PREPARE rb_insert(int,int) AS INSERT INTO rb_write(v) VALUES($1)", 0))
					insert = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_insert(12,99)"}
				}
				run(fixture.Exec("ALTER TABLE rb_write ADD unused INT", 0))
				preparedRebindFailure(t, p, b, returning, sqlPreparedDiagnostic("0A000", "cached plan must not change result type"))
				run(sqlPreparedScalar("SELECT v FROM rb_write", "v", "INT4", int64(7)))
				sqlPreparedStep(t, p, b, insert, fixture.Exec(insert.SQL, 1, args...))
				run(fixture.Select("SELECT v,unused FROM rb_write ORDER BY v", []string{"v", "unused"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}, {int64(12), nil}}))
				run(fixture.Exec("ALTER TABLE rb_write DROP unused", 0))
				preparedRebindRead(t, p, b, returning, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(8)}})
				// Fixed INT4 formal assignment to a newly TEXT destination.
				run(fixture.Exec("ALTER TABLE rb_write ALTER v TYPE TEXT USING v::text", 0))
				sqlPreparedStep(t, p, b, insert, fixture.Exec(insert.SQL, 1, args...))
				run(fixture.Select("SELECT v FROM rb_write ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"12"}, {"8"}, {"8"}}))
			})
		})
	}
}
