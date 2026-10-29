package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPreparedInstalledScalarMembership(t *testing.T) {
	for _, tc := range []struct {
		provider, family, input string
		slot                    int
	}{
		{"hstore", "hstore", `a=>NULL,b=>"NULL"`, 0},
		{"ltree", "lquery", "Top.*{0,2}", 0},
		{"ltree", "ltree", "Top.Countries.Europe", 3},
		{"ltree", "ltxtquery", "Europe & !Asia", 6},
		{"isn", "ean13", "1234567890120!", 0},
		{"isn", "isbn", "9780123456780!", 3},
		{"isn", "isbn13", "9780123456780!", 6},
		{"isn", "ismn", "M123456780!", 9},
		{"isn", "ismn13", "M123456780!", 12},
		{"isn", "issn", "9771234567898!", 15},
		{"isn", "issn13", "9771234567898!", 18},
		{"isn", "upc", "123456789010!", 21},
	} {
		t.Run(tc.family, func(t *testing.T) {
			for _, state := range []string{"detached", "foreign", "origin_absent"} {
				t.Run(state, func(t *testing.T) {
					e, ext := ownershipUnitInstall(t, tc.provider)
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					before := e.CatalogRead()
					base := before.MustType(ext.Installation.Members[tc.slot])
					value, err := base.SQLType.Input(context.Background(), tc.input, -1)
					if err != nil {
						t.Fatal(err)
					}
					original := []analyze.TemporalConstant{
						{Offset: 7, TypeID: base.SQLType.ID(), Typmod: -1, Typed: true, TypedTypmod: -1, Value: value},
						{Offset: 31, TypeID: base.SQLType.ID(), Typmod: -1, Value: sqltype.Null(base.SQLType)},
					}
					saved := append([]analyze.TemporalConstant(nil), original...)
					execute := func(q string) { enginetest.RoleExecute(t, e, op, q) }
					execute("ALTER EXTENSION " + tc.provider + " DROP TYPE public." + tc.family)
					if state == "foreign" {
						execute(`CREATE EXTENSION "uuid-ossp"`)
						execute(`ALTER EXTENSION "uuid-ossp" ADD TYPE public.` + tc.family)
					}
					// Real renames force a distinct nominal generation, distinguishing
					// reownership from the unchanged-pointer membership case.
					execute("ALTER TYPE public." + tc.family + " RENAME TO held_scalar")
					execute("ALTER TYPE public.held_scalar RENAME TO " + tc.family)
					if state == "origin_absent" {
						input := before.MustProcedure(ext.Installation.Members[tc.slot+1])
						output := before.MustProcedure(ext.Installation.Members[tc.slot+2])
						execute("ALTER EXTENSION " + tc.provider + " DROP FUNCTION public." + input.Name() + "(cstring)")
						execute("ALTER EXTENSION " + tc.provider + " DROP FUNCTION public." + output.Name() + "(public." + tc.family + ")")
						execute("DROP EXTENSION " + tc.provider)
					}
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					if err != nil {
						e.Mu.Unlock()
						t.Fatal(err)
					}
					path, err := core.EngineSequenceEnvironmentLocked(e, auth)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					current := auth.Catalog.MustType(base.Address())
					analysis := core.Analysis{
						Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken,
						Private: auth.Session.Transactions.Catalog, Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(),
						Types:        map[ids.ObjectAddress]*catalog.Type{base.Address(): base},
						Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}},
					}
					description := core.Description{Temporals: original, Analysis: &analysis, PlanGeneration: auth.Session.DiscardPlansGeneration}
					reown := func(a core.AuthorizationView, d *core.Description) []analyze.TemporalConstant {
						e.Mu.Lock()
						defer e.Mu.Unlock()
						return core.EnginePreparedInstalledScalarLiteralTemporalsLocked(e, a, d, path)
					}
					got := reown(auth, &description)
					if &got[0] == &original[0] || base.SQLType == current.SQLType {
						t.Fatal("real transition did not produce isolated current-generation candidate")
					}
					want, err := current.SQLType.Input(context.Background(), tc.input, -1)
					if err != nil {
						t.Fatal(err)
					}
					if got[0].Value.Type() != current.SQLType || !sqltype.SameDatum(got[0].Value, want) || !sqltype.SameDatum(got[1].Value, sqltype.Null(current.SQLType)) || !reflect.DeepEqual(original, saved) {
						t.Fatal("candidate changed payload/NULL or retained source")
					}
					if _, err := value.Retag(current.SQLType); err == nil {
						t.Fatal("ordinary Retag acquired candidate authority")
					}
					for _, negative := range []string{"copied_origin", "principal", "epoch", "domain", "aggregate", "codec"} {
						t.Run(negative, func(t *testing.T) {
							a, d, local := auth, description, analysis
							d.Analysis = &local
							local.Dependencies = append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
							state := catalog.CloneState(auth.Catalog.State())
							bad := *current
							switch negative {
							case "copied_origin":
								copied := *bad.Origin.Installation
								origin := *bad.Origin
								origin.Installation = &copied
								bad.Origin = &origin
							case "principal":
								bad.Owner = catalog.Type{}.Owner
							case "epoch":
								local.Dependencies[0].Ref = e.Catalog.Domain.Ref(auth.Catalog.Epoch()+1, base.Address())
							case "domain":
								local.Dependencies[0].Ref = new(ids.Domain).Ref(auth.Catalog.Epoch(), base.Address())
							case "aggregate":
								local.Dependencies[0].Purpose = catalog.AggregateSupportDependency
							case "codec":
								codec := *auth.Catalog.MustProcedure(ext.Installation.Members[tc.slot+1])
								codec.Strict = false
								state.SetDescriptor(codec.Address(), &codec)
							}
							state.SetDescriptor(bad.Address(), &bad)
							a.Catalog = e.Catalog.Read(state)
							if rejected := reown(a, &d); &rejected[0] != &original[0] || !reflect.DeepEqual(rejected, saved) {
								t.Fatal("invalid current/captured authority imported datum")
							}
						})
					}
				})
			}
		})
	}
}
