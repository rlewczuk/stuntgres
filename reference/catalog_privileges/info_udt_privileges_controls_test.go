package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func uvResetSource(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := &uvCase{&ugCase{t: t, p: p, b: b, recorder: b.Recorder}}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_user,current_database()").Scan(&c.owner, &c.database); err != nil {
				t.Fatal(err)
			}
			create := func() { c.run(b.DB, fixture.Exec("CREATE TYPE public.uv_reset AS(a integer)", 0)) }
			read := func() { c.check(b.DB, "public", "uv_reset", c.baseline(c.owner, "public", "uv_reset")...) }
			create()
			read()
			// Every direct/fresh read has closed its rows and statement. No
			// borrowed connection or held handle crosses catalog replacement.
			if !b.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				c.check(b.DB, "public", "uv_reset")
				create()
				read()
			}
		})
	}
}
