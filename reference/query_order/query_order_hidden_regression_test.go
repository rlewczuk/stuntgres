package reference_test

import (
	"slices"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderRepeatedHiddenVolatileRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE v(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO v VALUES(1),(2),(3)", 3))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			where string
			rows  [][]any
			next  int64
		}{
			{"", [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, 4},
			{"WHERE id>1", [][]any{{int64(2)}, {int64(3)}}, 3},
		} {
			p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
			step := fixture.Select("SELECT id FROM v "+tc.where+" ORDER BY nextval('s'),nextval('s') DESC", []string{"id"}, []string{"INT4"}, tc.rows)
			// Input traversal is not an oracle. The admitted bag and subsequent
			// sequence value independently distinguish shared SQL99 keys.
			step.Bag = true
			for _, backend := range p.Backends() {
				func() {
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(p.Ctx, step.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{backend.DB, stmt, step.Query}
					}
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
					next := fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{tc.next}})
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, next)
					if prepared {
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
						next.Rows = [][]any{{2 * tc.next}}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, next)
					}
				}()
			}
			p.Step(t, fixture.Exec("DROP SEQUENCE s", 0))
		}
	}
}

func TestReferenceQueryOrderRepeatedHiddenPreparedFreshness(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE v(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO v VALUES(1),(2),(3),(4),(5)", 5))
	p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT id FROM v WHERE id>2 ORDER BY nextval('s'),nextval('s') DESC")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, nextWant := range []int64{4, 8} {
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				names, err := rows.Columns()
				if err != nil || !slices.Equal(names, []string{"id"}) {
					rows.Close()
					t.Fatal(names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					rows.Close()
					t.Fatal(types, err)
				}
				var ids []int64
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				slices.Sort(ids)
				if err != nil || !slices.Equal(ids, []int64{3, 4, 5}) {
					t.Fatal(ids, err)
				}
				var next int64
				if err := backend.DB.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != nextWant {
					t.Fatal(next, nextWant, err)
				}
			}
		})
	}
}
