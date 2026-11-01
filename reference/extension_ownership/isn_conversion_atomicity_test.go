package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnConversionAtomicity(t *testing.T) {
	for _, target := range isnConversionRoles[1:] {
		t.Run(target.name, func(t *testing.T) {
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE isn_atomic(id integer NOT NULL,v "+target.name+")", 0))
				run(fixture.Exec("INSERT INTO isn_atomic VALUES(1,"+isnConversionLiteral(target.input)+")", 1))
				seed := fixture.Select("SELECT id,v::ean13 = "+isnConversionLiteral(target.compact)+"::ean13 AS intact FROM isn_atomic ORDER BY id", []string{"id", "intact"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
				identities := `SELECT p.oid::bigint,p.proargtypes::text,p.prorettype::bigint,p.proowner::bigint FROM pg_proc p WHERE p.pronamespace='public'::regnamespace ORDER BY p.oid`
				original := fixture.OwnershipSnapshot(t, p, c, identities)
				var saved string
				if !b.PG {
					saved = fixture.ExtensionDump(t, p.Ctx, p.Native)
				}
				want := isnConversionDiagnostic("22P02", "cannot cast EAN13 to "+isnConversionInputFamily(target.name)+` for number: "1234567890128"`)
				bound := "INSERT INTO isn_atomic VALUES(2,$1::ean13::" + target.name + "),(3,$2::ean13::" + target.name + ")"
				held := fixture.IdentityHeld(t, p.Ctx, c, bound)
				defer held.Statement.Close()
				for _, q := range []string{
					"INSERT INTO isn_atomic VALUES(2," + isnConversionLiteral(target.compact) + "::ean13::" + target.name + "),(3,'1234567890128'::ean13::" + target.name + ")",
					"INSERT INTO isn_atomic SELECT id,v::" + target.name + " FROM (VALUES(2," + isnConversionLiteral(target.compact) + "::ean13),(3,'1234567890128'::ean13)) AS input(id,v)",
					bound,
				} {
					var args []any
					if q == bound {
						args = []any{target.compact, []byte("1234567890128")}
					}
					for _, prepared := range []bool{false, true} {
						s := isnConversionRuntimeError(q, want, args...)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						run(seed)
						fixture.OwnershipUnchanged(t, p, c, identities, original)
						if !b.PG && fixture.ExtensionDump(t, p.Ctx, p.Native) != saved {
							t.Fatal("failed conversion INSERT changed committed dump")
						}
					}
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, isnConversionRuntimeError(bound, want, target.compact, "1234567890128"))
				run(seed)
				fixture.OwnershipUnchanged(t, p, c, identities, original)
				if !b.PG && fixture.ExtensionDump(t, p.Ctx, p.Native) != saved {
					t.Fatal("failed held conversion INSERT changed committed dump")
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(bound, 2, target.compact, []byte(target.compact+"!")))
				run(fixture.Select("SELECT id,v::ean13 = "+isnConversionLiteral(target.compact)+"::ean13 AS unmarked,v::ean13 = "+isnConversionLiteral(target.compact+"!")+"::ean13 AS marked FROM isn_atomic ORDER BY id", []string{"id", "unmarked", "marked"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), true, false}, {int64(3), false, true}}))
				if b.PG {
					return
				}
				// Native Go cancellation is not a PostgreSQL diagnostic. These
				// routes exercise the public boundaries, not timing-dependent SQL.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, q := range []string{
					"SELECT $1::ean13::" + target.name,
					"SELECT $1::" + target.name + " = $1::" + target.name,
					"SELECT v FROM isn_atomic ORDER BY v",
				} {
					var args []any
					if q != "SELECT v FROM isn_atomic ORDER BY v" {
						args = []any{target.compact}
					}
					rows, err := c.QueryContext(ctx, q, args...)
					if rows != nil {
						rows.Close()
						t.Fatal("pre-canceled query exposed rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("ordinary cancellation got %T %v", err, err)
					}
				}
				fixture.OwnershipUnchanged(t, p, c, identities, original)
			})
		})
	}
}
