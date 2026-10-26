package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func iccControlSeed(c *iccCase) [][]any {
	c.exec("CREATE TABLE icc_controls_a(v integer CONSTRAINT icc_controls CHECK(v>0))", "CREATE TABLE icc_controls_b(v integer CONSTRAINT icc_controls CHECK(v>0))", "CREATE DOMAIN icc_controls_d AS integer CONSTRAINT icc_controls_nn NOT NULL CONSTRAINT icc_controls_dom CHECK(VALUE>0)")
	a := c.row("public", "icc_controls", "(v > 0)")
	return [][]any{a, a, c.row("public", "icc_controls_dom", "(VALUE > 0)"), c.row("public", "icc_controls_nn", "VALUE IS NOT NULL")}
}

func iccRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	count := 0
	for rows.Next() {
		var cells [4]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3]); err != nil {
			t.Fatal(err)
		}
		matched := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(cells[:], expected) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected retained row %v", cells)
		}
		count++
	}
	if rows.Err() != nil || count != len(want) {
		t.Fatalf("retained count=%d want=%d error=%v", count, len(want), rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceInfoCheckConstraintsNativeControls(t *testing.T) {
	for _, phase := range []string{"same_engine_restore", "independent_target_restore", "canceled_reads"} {
		t.Run(phase, func(t *testing.T) {
			iccScenario(t, func(c *iccCase) {
				want := iccControlSeed(c)
				c.consumer()(want...)
				iccLogicalShape(c)
				c.run(fixture.Select("SELECT count(DISTINCT oid) AS constraints,count(DISTINCT conrelid) AS relations FROM pg_constraint WHERE conname='icc_controls'", []string{"constraints", "relations"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
				if c.b.PG {
					return // PostgreSQL has no process-local native control API.
				}
				if phase == "canceled_reads" {
					statement, err := c.conn.PrepareContext(c.p.Ctx, iccQuery)
					if err != nil {
						c.t.Fatal(err)
					}
					defer statement.Close()
					ctx, cancel := context.WithCancel(c.p.Ctx)
					cancel()
					for _, x := range []fixture.Executor{c.conn, fixture.HeldStatement{Executor: c.conn, Statement: statement, SQL: iccQuery}} {
						rows, err := x.QueryContext(ctx, iccQuery)
						if rows != nil {
							rows.Close()
							c.t.Fatal("canceled read published rows")
						}
						var state interface{ SQLState() string }
						if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
							c.t.Fatalf("ordinary canceled read=%v", err)
						}
					}
					c.read(want...)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				for _, token := range []string{"icc_controls_a", "icc_controls_b", "icc_controls_d", "icc_controls_dom", "icc_controls_nn", "CHECK", "NOT NULL"} {
					if !strings.Contains(dump.String(), token) {
						c.t.Fatalf("actual dump omits %q: %s", token, dump.String())
					}
				}
				if phase == "same_engine_restore" {
					retained, err := c.conn.QueryContext(c.p.Ctx, iccQuery)
					if err != nil {
						c.t.Fatal(err)
					}
					defer retained.Close()
					columns, err := retained.ColumnTypes()
					if err != nil {
						c.t.Fatal(err)
					}
					iccMetadata(c.t, columns, false)
					if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
						c.t.Fatal(err)
					}
					c.read()
					c.run(c.step("SELECT * FROM information_schema.check_constraints WHERE constraint_schema='information_schema' AND constraint_name='cardinal_number_domain_check'", [][]any{c.row("information_schema", "cardinal_number_domain_check", "(VALUE >= 0)")}))
					if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						c.t.Fatal(err)
					}
					c.consumer()(want...)
					iccLogicalShape(c)
					iccRetained(c.t, retained, want)
					iccMetadata(c.t, columns, false)
					return
				}
				target := fixture.NewFixture(c.t, false, true)
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					c.t.Fatal(err)
				}
				var database string
				if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					c.t.Fatal(err)
				}
				targetRows := make([][]any, len(want))
				for i, row := range want {
					targetRows[i] = append([]any(nil), row...)
					targetRows[i][0] = database
				}
				checkTarget := func() {
					s := c.step(iccQuery, targetRows)
					fixture.RunStep(c.t, target.Ctx, target.Memory, target.MemoryRecord, s)
					fixture.RunStep(c.t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(s))
					fixture.RunStep(c.t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT count(DISTINCT oid) AS constraints,count(DISTINCT conrelid) AS relations FROM pg_constraint WHERE conname='icc_controls'", []string{"constraints", "relations"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
				}
				checkTarget()
				bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t(v staged.d); INSERT INTO staged.t VALUES(-1); ALTER DOMAIN staged.d ADD CONSTRAINT positive CHECK(missing>0) NOT VALID;"
				fixture.State(c.t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), "42703")
				checkTarget()
				fixture.RunStep(c.t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='staged'", []string{"nspname"}, []string{"NAME"}, nil))
				ctx, cancel := context.WithCancel(target.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					c.t.Fatalf("canceled Restore=%v", err)
				}
				checkTarget()
			})
		})
	}
}
