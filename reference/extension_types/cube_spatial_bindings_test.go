package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeSpatialBindings(t *testing.T) {
	cubeSpatialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeSpatialSeed(t, p, b, c)
		cubeSpatialBoth(t, p, b, c, cubeSpatialTableCheck(b))
		for i, surface := range cubeSpatialSurfaces {
			t.Run(surface, func(t *testing.T) {
				q := "SELECT " + cubeSpatialExpression(surface, "$1::cube", "$2::cube") + " AS result"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				want := cubeSpatialResult(false, true, false, 5, 7, 4)[i]
				for _, args := range [][]any{
					{"(3,4)", "()"}, {[]byte("(3,4)"), []byte("()")},
					{nil, "()"}, {"(3,4)", nil}, {nil, nil},
					{[]byte(nil), "()"}, {"(3,4)", []byte(nil)}, {[]byte(nil), []byte(nil)},
				} {
					value := want
					if args[0] == nil || args[1] == nil {
						value = nil
					}
					for _, arg := range args {
						if v, ok := arg.([]byte); ok && v == nil {
							value = nil
						}
					}
					step := cubeSpatialSelect(b, q, []string{"result"}, []string{cubeSpatialTypes[i]}, [][]any{{value}}, args...)
					cubeSpatialBoth(t, p, b, c, step)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
				}
				// One typed operand supplies unambiguous context to the unknown.
				for _, pair := range [][2]string{{"'(3,4)'::cube", "'()'"}, {"'(3,4)'", "'()'::cube"}} {
					unknown := "SELECT " + cubeSpatialExpression(surface, pair[0], pair[1]) + " AS result"
					cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, unknown, []string{"result"}, []string{cubeSpatialTypes[i]}, [][]any{{want}}))
				}
				contextual := "SELECT " + cubeSpatialExpression(surface, "$1", "$2::cube") + " AS result"
				contextStmt, err := c.PrepareContext(p.Ctx, contextual)
				if err != nil {
					t.Fatal(err)
				}
				defer contextStmt.Close()
				for _, args := range [][]any{{"(3,4)", "()"}, {[]byte("(3,4)"), []byte("()")}, {nil, "()"}} {
					value := want
					if args[0] == nil {
						value = nil
					}
					step := cubeSpatialSelect(b, contextual, []string{"result"}, []string{cubeSpatialTypes[i]}, [][]any{{value}}, args...)
					cubeSpatialBoth(t, p, b, c, step)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: contextStmt, SQL: contextual}, b.Recorder, step)
				}
				for _, pair := range [][2]string{{"NULL::cube", "'()'::cube"}, {"'(3,4)'::cube", "NULL::cube"}, {"NULL::cube", "NULL::cube"}} {
					nullQuery := "SELECT " + cubeSpatialExpression(surface, pair[0], pair[1]) + " AS result"
					cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, nullQuery, []string{"result"}, []string{cubeSpatialTypes[i]}, [][]any{{nil}}))
				}
			})
		}
		for _, suffix := range []string{" WHERE id=4", " WHERE id<0"} {
			rows := [][]any{{nil, nil, nil, nil, nil, nil, nil}}
			if suffix == " WHERE id<0" {
				rows = [][]any{}
			}
			cubeSpatialBoth(t, p, b, c, cubeSpatialNamedSelect(b, "SELECT "+cubeSpatialProjection("a", "b")+" FROM cube_spatial_data"+suffix, rows))
		}
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT id FROM cube_spatial_data WHERE a && b ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		ordered := []int{1, 0, 2, 3}
		var want [][]any
		for _, i := range ordered {
			want = append(want, cubeSpatialFixtureRows()[i])
		}
		cubeSpatialBoth(t, p, b, c, cubeSpatialSelect(b, "SELECT id,"+cubeSpatialProjection("a", "b")+" FROM cube_spatial_data ORDER BY a <-> b NULLS LAST,id", append([]string{"id"}, cubeSpatialNames...), append([]string{"INT4"}, cubeSpatialTypes...), want))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE cube_spatial_computed(id integer,hit boolean,d float8)", 0))
		run(fixture.Exec("INSERT INTO cube_spatial_computed SELECT id,a && b,cube_distance(a,b) FROM cube_spatial_data", 4))
		run(fixture.Exec("UPDATE cube_spatial_computed SET hit='(1)'::cube @> '(1)'::cube,d='(3,4)'::cube <#> '()'::cube WHERE id=1", 1))
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT id,hit,d FROM cube_spatial_computed ORDER BY id", []string{"id", "hit", "d"}, []string{"INT4", "BOOL", "FLOAT8"}, [][]any{{int64(1), true, float64(7)}, {int64(2), true, float64(0)}, {int64(3), false, float64(5)}, {int64(4), nil, nil}}))
		// Block ordinary feature SELECT admission instead of manufacturing an
		// unbounded arithmetic workload. The fixture observes the actual wait,
		// cancels and joins it, and asserts PG57014 versus ordinary native context
		// cancellation. Each route then reuses the same healthy physical session.
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "active_direct", true: "active_held"}[prepared], func(t *testing.T) {
				q := cubeSpatialTableQuery()
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE cube_spatial_data IN ACCESS EXCLUSIVE MODE", 0))
				fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if stmt != nil {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				cubeSpatialBoth(t, p, b, c, cubeSpatialTableCheck(b))
				if stmt != nil {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, cubeSpatialTableCheck(b))
				}
			})
		}
		if !b.PG {
			// Geometry is bounded to100 dimensions, so no synthetic long-running
			// geometry query is introduced just to manufacture active cancellation.
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			q := "SELECT " + cubeSpatialProjection("$1::cube", "$2::cube")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, query := range []func() (*sql.Rows, error){
				func() (*sql.Rows, error) { return c.QueryContext(ctx, q, "(3,4)", "()") },
				func() (*sql.Rows, error) { return stmt.QueryContext(ctx, "(3,4)", "()") },
			} {
				rows, err := query()
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("precancel %v want context.Canceled", err)
				}
			}
			cubeSpatialBoth(t, p, b, c, cubeSpatialTableCheck(b))
		}
	})
}
