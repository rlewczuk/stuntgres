package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTransformsDiagnostics(t *testing.T) {
	trScenario(t, func(c *trCase) {
		var identity int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT 'information_schema.transforms'::regclass::oid::bigint").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		unchanged := func() {
			c.inventory(c.conn)
			c.run(c.conn, fixture.Select("SELECT 'information_schema.transforms'::regclass::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{identity}}))
		}
		for _, tc := range []struct {
			q        string
			position int
		}{
			{"SELECT absent FROM information_schema.transforms", 8},
			{"SELECT /*é*/ absent FROM information_schema.transforms", 14},
			{"SELECT absent FROM information_schema.transforms WHERE false", 8},
		} {
			d := trDiagnostic("42703", `column "absent" does not exist`)
			d.Position = fixture.Position(tc.position)
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			c.run(c.conn, s)
			unchanged()
		}
		for _, tc := range []struct{ operation, action, q string }{
			{"INSERT", "inserting into the view", "INSERT INTO information_schema.transforms VALUES ('a','b','c','d','e','f','g','FROM SQL')"},
			{"UPDATE", "updating the view", "UPDATE information_schema.transforms SET transform_type='x' WHERE false"},
			{"DELETE", "deleting from the view", "DELETE FROM information_schema.transforms WHERE false"},
		} {
			message := `cannot update view "transforms"`
			if tc.operation == "INSERT" {
				message = `cannot insert into view "transforms"`
			}
			if tc.operation == "DELETE" {
				message = `cannot delete from view "transforms"`
			}
			d := trDiagnostic("55000", message)
			d.Detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
			d.Hint = "To enable " + tc.action + ", provide an INSTEAD OF " + tc.operation + " trigger or an unconditional ON " + tc.operation + " DO INSTEAD rule."
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			unchanged()
			s.Prepared = true
			s.ErrorStage = "prepare"
			c.run(c.conn, s)
			unchanged()
		}
		for _, tc := range []struct {
			q, message string
			position   int
		}{
			{"UPDATE information_schema.transforms SET absent='x' WHERE false", `column "absent" of relation "transforms" does not exist`, 42},
			{"DELETE FROM information_schema.transforms WHERE absent='x'", `column "absent" does not exist`, 49},
		} {
			d := trDiagnostic("42703", tc.message)
			d.Position = fixture.Position(tc.position)
			s := fixture.ErrorDiagnostic(tc.q, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			c.run(c.conn, s)
			unchanged()
		}
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			held := c.hold(trQuery)
			rows, err := c.conn.QueryContext(ctx, trQuery)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled direct", rows, err)
			}
			rows, err = held.Statement.QueryContext(ctx)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("pre-canceled prepared", rows, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("context error has SQLSTATE", err)
			}
			unchanged()
		}
	})
}
