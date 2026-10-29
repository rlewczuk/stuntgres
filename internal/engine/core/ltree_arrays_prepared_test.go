package core_test

import (
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestLtreeArrayPreparedOwnership(t *testing.T) {
	t.Run("accepted_bindings", ltreeArrayAcceptedOwnership)
	t.Run("narrow_shape_eligibility", ltreeArrayPreparedShapes)
	t.Run("stored_generations", ltreeArrayStoredGenerations)
	for _, family := range []string{"ltree", "lquery"} {
		t.Run(family, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE EXTENSION ltree")
			base := domainUnitType(t, e.CatalogRead(), family)
			array := domainUnitType(t, e.CatalogRead(), "_"+family)
			value, err := array.SQLType.Input(t.Context(), "[7:8]={a,NULL}", -1)
			if err != nil {
				t.Fatal(err)
			}
			rank2, err := array.SQLType.Input(t.Context(), "{{a,b}}", -1)
			if err != nil {
				t.Fatal(err)
			}
			empty, err := array.SQLType.Input(t.Context(), "{}", -1)
			if err != nil {
				t.Fatal(err)
			}
			original := []analyze.TemporalConstant{{Offset: 7, TypeID: array.SQLType.ID(), Typmod: -1, Value: value}, {Offset: 31, TypeID: array.SQLType.ID(), Typmod: -1, Value: sqltype.Null(array.SQLType)}, {Offset: 43, TypeID: array.SQLType.ID(), Typmod: -1, Value: rank2}, {Offset: 51, TypeID: array.SQLType.ID(), Typmod: -1, Value: empty}, {Offset: 59, TypeID: sqltype.Date, Typmod: -1, Scalar: 42}}
			saved := append([]analyze.TemporalConstant(nil), original...)
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
			analysis := core.Analysis{Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken, Private: auth.Session.Transactions.Catalog, Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(), Types: map[ids.ObjectAddress]*catalog.Type{base.Address(): base, array.Address(): array}, Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}, {Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), array.Address()), Revision: array.Revision()}}}
			expected := core.Description{Temporals: original, Analysis: &analysis, PlanGeneration: auth.Session.DiscardPlansGeneration}
			remap := func(d *core.Description) []analyze.TemporalConstant {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				return core.EnginePreparedInstalledScalarLiteralTemporalsLocked(e, auth, d, path)
			}
			if got := remap(&expected); &got[0] != &original[0] {
				t.Fatal("live captures copied")
			}
			// A genuine type rename creates new scalar/array descriptor generations but
			// keeps installation identity. Import must share entered scalar backing.
			enginetest.RoleExecute(t, e, op, "ALTER EXTENSION ltree DROP TYPE "+family)
			enginetest.RoleExecute(t, e, op, "ALTER TYPE "+family+" RENAME TO la_prepared_renamed")
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := remap(&expected)
			if len(got) != 5 || &got[0] == &original[0] || got[0].Value.Type() == array.SQLType {
				t.Fatal("array capture not attempt-locally reminted")
			}
			current := e.CatalogRead().MustType(array.Address()).SQLType
			for i, capture := range got[:4] {
				if capture.Value.Type() != current || !reflect.DeepEqual(capture.Value.ArrayDimensions(), original[i].Value.ArrayDimensions()) {
					t.Fatal("outer generation/rank/bounds not reminted")
				}
				for j := range original[i].Value.ArrayLen() {
					entry := capture.Value.ArrayElement(j)
					old := original[i].Value.ArrayElement(j)
					if entry.Type() != current.Element() || entry.IsNull() != old.IsNull() {
						t.Fatal("element/NULL source generation retained")
					}
					var back sqltype.Value
					if family == "ltree" {
						back, err = entry.RemapLtree(base.SQLType)
					} else {
						back, err = entry.RemapLquery(base.SQLType)
					}
					if err != nil || back != old {
						t.Fatal("compiled backing changed", err)
					}
				}
			}
			if !reflect.DeepEqual(expected.Temporals, saved) {
				t.Fatal("candidate published before acceptance")
			}
			genuine := auth.Catalog
			for _, cause := range []string{"role", "association", "implementation"} {
				candidate := catalog.CloneState(genuine.State())
				switch cause {
				case "role":
					changed := *genuine.MustType(base.Address())
					origin := *changed.Origin
					origin.Declaration ^= 3
					changed.Origin = &origin
					candidate.SetDescriptor(changed.Address(), &changed)
				case "association":
					changed := *genuine.MustType(base.Address())
					changed.Array++
					candidate.SetDescriptor(changed.Address(), &changed)
				case "implementation":
					changed := *genuine.MustType(base.Address())
					origin := *changed.Origin
					installation := *origin.Installation
					installation.Implementation = nil
					origin.Installation = &installation
					changed.Origin = &origin
					candidate.SetDescriptor(changed.Address(), &changed)
				}
				auth.Catalog = e.Catalog.Read(candidate)
				if unchanged := remap(&expected); &unchanged[0] != &original[0] || !reflect.DeepEqual(unchanged, saved) {
					t.Fatal("forged current role/association/manifest imported captures", cause)
				}
				auth.Catalog = genuine
			}
			for _, change := range []func(*core.Description, *core.Analysis){
				func(_ *core.Description, a *core.Analysis) {
					foreign := (&authorization.Domain{}).Principal(base.Owner.ID())
					scalar, companion := *base, *array
					scalar.Owner, companion.Owner = foreign, foreign
					a.Types = map[ids.ObjectAddress]*catalog.Type{base.Address(): &scalar, array.Address(): &companion}
				},
				func(_ *core.Description, a *core.Analysis) {
					a.Types = map[ids.ObjectAddress]*catalog.Type{array.Address(): array}
				},
				func(_ *core.Description, a *core.Analysis) { a.Dependencies = a.Dependencies[1:] },
				func(_ *core.Description, a *core.Analysis) {
					a.Types = map[ids.ObjectAddress]*catalog.Type{base.Address(): base}
				},
				func(_ *core.Description, a *core.Analysis) { a.Dependencies = a.Dependencies[:1] },
				func(_ *core.Description, a *core.Analysis) { a.Epoch++ },
				func(_ *core.Description, a *core.Analysis) { a.Namespace++ },
				func(_ *core.Description, a *core.Analysis) { a.NeedsReanalysis = true },
				func(d *core.Description, _ *core.Analysis) { d.Temporals[0].TypeID++ },
				func(d *core.Description, _ *core.Analysis) {
					foreign, err := sqltype.Array(array.SQLType.ID(), array.Name(), base.SQLType)
					if err != nil {
						t.Fatal(err)
					}
					d.Temporals[0].Value = sqltype.Null(foreign)
				},
				func(_ *core.Description, a *core.Analysis) {
					wrong := *base
					wrong.Array++
					a.Types = map[ids.ObjectAddress]*catalog.Type{base.Address(): &wrong, array.Address(): array}
				},
				func(_ *core.Description, a *core.Analysis) {
					a.Dependencies = []catalog.ExpressionDependency{{Ref: new(ids.Domain).Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}, {Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), array.Address()), Revision: array.Revision()}}
				},
				func(d *core.Description, _ *core.Analysis) { d.PlanGeneration++ },
			} {
				a, d := analysis, expected
				d.Analysis = &a
				d.Temporals = append([]analyze.TemporalConstant(nil), original...)
				change(&d, &a)
				before := append([]analyze.TemporalConstant(nil), d.Temporals...)
				got := remap(&d)
				if &got[0] != &d.Temporals[0] || !reflect.DeepEqual(got, before) {
					t.Fatal("forged source/failed candidate imported captures")
				}
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			if err := stage.Remove(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), array.Address())); err != nil {
				t.Fatal(err)
			}
			auth.Catalog = e.StageRead(stage)
			retire := func(d *core.Description) []analyze.TemporalConstant {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				return core.EngineRetirePreparedInstalledScalarLiteralTemporalsLocked(e, auth, d, path, d.Temporals)
			}
			retired := retire(&expected)
			if len(retired) != 1 || !reflect.DeepEqual(retired[0], original[4]) || &retired[0] == &original[4] {
				t.Fatal("absent array not retired into isolated candidate")
			}
			a, d := analysis, expected
			d.Analysis = &a
			a.Types = map[ids.ObjectAddress]*catalog.Type{array.Address(): array}
			if unchanged := retire(&d); &unchanged[0] != &original[0] {
				t.Fatal("missing scalar authority retired array")
			}
			if !reflect.DeepEqual(original, saved) || analysis.NeedsReanalysis {
				t.Fatal("failed publication mutated accepted source")
			}
		})
	}
}
