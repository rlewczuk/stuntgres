package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprVirtualBoundUpdate(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE array_pair AS (a integer[])`,
			`CREATE TABLE destination(k integer,v array_pair)`,
			`CREATE TABLE virtual_source(k integer,g integer GENERATED ALWAYS AS (k+1) VIRTUAL)`,
			`CREATE TABLE recipe_source(k integer,g integer GENERATED ALWAYS AS (2/(11-k)) VIRTUAL)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO destination VALUES(1,NULL),(3,NULL),(7,ROW(ARRAY[7])),(10,ROW(ARRAY[5])),(11,ROW(ARRAY[6])),(99,ROW(ARRAY[99]))`, 6))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO virtual_source(k) VALUES(1)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO recipe_source(k) VALUES(10),(11)`, 2))

		// The virtual read occurs only below the composite target. Neither the
		// predicate nor the literal RHS can accidentally enable source demand.
		const direct = `UPDATE destination d SET v.a[s.g]=9 FROM virtual_source s WHERE d.k=s.k`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(direct, 1))
		check := func(want [][]any) {
			compositeExprBoth(t, p, b, c, `SELECT k,(v).a::text AS a FROM destination ORDER BY k`, []string{"k", "a"}, []string{"INT4", "TEXT"}, want)
		}
		initial := [][]any{{int64(1), "[2:2]={9}"}, {int64(3), nil}, {int64(7), "{7}"}, {int64(10), "{5}"}, {int64(11), "{6}"}, {int64(99), "{99}"}}
		check(initial)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE destination SET v=NULL WHERE k=1`, 1))
		const heldSQL = `UPDATE destination d SET v.a[s.g]=9 FROM virtual_source s WHERE d.k=s.k AND d.k=$1`
		held, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		result, err := held.ExecContext(p.Ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		check(initial)

		// Reuse the same handle with a different physical source value and key.
		// The newly formed array's lower bound must come from this execution.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE virtual_source SET k=3 WHERE k=1`, 1))
		result, err = held.ExecContext(p.Ctx, 3)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		fresh := [][]any{{int64(1), "[2:2]={9}"}, {int64(3), "[4:4]={9}"}, {int64(7), "{7}"}, {int64(10), "{5}"}, {int64(11), "{6}"}, {int64(99), "{99}"}}
		check(fresh)
		result, err = held.ExecContext(p.Ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 0)
		check(fresh)

		// A genuine null-extended source must produce a NULL bound, not run a
		// recipe using another occurrence or mistake NULL fields for presence.
		const absent = `UPDATE destination d SET v.a[s.g]=9 FROM (VALUES(7)) a(k) LEFT JOIN virtual_source s ON a.k=s.k WHERE d.k=a.k`
		fixture.CompositeReferenceError(t, p, c, absent, "execute", fixture.CompositeReferenceDiagnostic("22004", "array subscript in assignment must not be null"))
		check(fresh)

		// A later failing recipe leaves every target and the unrelated sentinel
		// unchanged on direct/held Exec and Query routes. Preparation is quiet.
		const failing = `UPDATE destination d SET v.a[s.g]=9 FROM recipe_source s WHERE d.k=s.k`
		division := fixture.CompositeReferenceDiagnostic("22012", "division by zero")
		fixture.CompositeReferenceError(t, p, c, failing, "execute", division)
		check(fresh)
		recovery, err := c.PrepareContext(p.Ctx, failing)
		if err != nil {
			t.Fatal(err)
		}
		defer recovery.Close()
		result, err = recovery.ExecContext(p.Ctx)
		if result != nil {
			t.Error("failing held recipe exposed result")
		}
		fixture.AssertDiagnostic(t, err, &division)
		check(fresh)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM recipe_source WHERE k=11`, 1))
		result, err = recovery.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		check([][]any{{int64(1), "[2:2]={9}"}, {int64(3), "[4:4]={9}"}, {int64(7), "{7}"}, {int64(10), "{5,9}"}, {int64(11), "{6}"}, {int64(99), "{99}"}})
	})
}
