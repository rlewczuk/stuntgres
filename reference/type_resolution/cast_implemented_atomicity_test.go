package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// A typed source column prevents immutable parameter folding at Bind from
// masquerading as runtime DML statement-atomic publication. The first source
// row is valid, the later row/member fails, and the exact sentinel survives.
func TestReferenceCastImplementedAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, source, target, good, bad, code, message string }{
		{"uuid", "text", "uuid", "00000000-0000-0000-0000-000000000001", "bad", "22P02", `invalid input syntax for type uuid: "bad"`},
		{"instant", "text", "timestamptz", "2000-01-01 00:00:00+00", "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`},
		{"array_leaf", "text[]", "uuid[]", `{00000000-0000-0000-0000-000000000001,NULL}`, `{00000000-0000-0000-0000-000000000001,bad}`, "22P02", `invalid input syntax for type uuid: "bad"`},
		{"json_kind", "jsonb", "int4", "2.5", "{}", "22023", `cannot cast jsonb object to type integer`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
					run(fixture.Exec("CREATE TABLE cast_atomic_source(id integer,s "+tc.source+")", 0))
					run(fixture.Exec("INSERT INTO cast_atomic_source VALUES(1,$1),(2,$2),(3,NULL)", 3, tc.good, tc.bad))
					run(fixture.Exec("CREATE TABLE cast_atomic_target(id integer,v "+tc.target+")", 0))
					// Sentinel creation uses the already available input codec, not the new cast.
					sentinel := castInventoryType(tc.target).seed
					if tc.target == "int4" {
						sentinel = "99"
					}
					run(fixture.Exec("INSERT INTO cast_atomic_target VALUES(99,$1)", 1, sentinel))
					target := castInventoryType(tc.target)
					sentinelValue := castExpectedValue(t, target, sentinel, b.PG)
					unchanged := func() {
						s := castResultStep(t, "SELECT v AS v FROM cast_atomic_target WHERE id=99", target, sentinelValue, b.PG)
						run(s)
						run(fixture.Select("SELECT id FROM cast_atomic_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
						run(fixture.Select("SELECT id FROM cast_atomic_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					}
					for _, form := range []string{"CAST(s AS " + tc.target + ")"} {
						q := "INSERT INTO cast_atomic_target SELECT id," + form + " FROM cast_atomic_source WHERE id<3 ORDER BY id"
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
						for _, x := range []fixture.Executor{b.DB, held} {
							bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
							bad.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, x, b.Recorder, bad)
							unchanged()
							// Repeating on the same held object proves recovery without a hidden
							// newly prepared plan or silently discarded second-row work.
							fixture.RunStep(t, p.Ctx, x, b.Recorder, bad)
							unchanged()
						}
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
						nullQ := fmt.Sprintf("SELECT %s AS v FROM cast_atomic_source WHERE id=3", form)
						castWitnessRoutes(t, p, b, castResultStep(t, nullQ, target, nil, b.PG))
						empty := castResultStep(t, fmt.Sprintf("SELECT %s AS v FROM cast_atomic_source WHERE false", form), target, nil, b.PG)
						empty.Rows = nil
						run(empty)
					}
				})
			}
		})
	}
}
