package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestLqueryPreparedLiteralRetirement(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION ltree")
	base := domainUnitType(t, e.CatalogRead(), "lquery")
	value, err := base.SQLType.Input(t.Context(), "foo*@%", -1)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := sqltype.Lquery(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	original := []analyze.TemporalConstant{
		{Offset: 7, TypeID: base.SQLType.ID(), Typmod: -1, Typed: true, TypedTypmod: -1, Value: value},
		{Offset: 31, TypeID: base.SQLType.ID(), Typmod: -1, Value: sqltype.Null(base.SQLType)},
		{Offset: 59, TypeID: sqltype.Date, Typmod: -1, Scalar: 42},
	}
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
	analysis := core.Analysis{
		Domain: e.Catalog.Domain, Epoch: auth.Catalog.Epoch(), Session: auth.Session.SessionToken,
		Private: auth.Session.Transactions.Catalog, Path: path, Namespace: auth.Catalog.State().NamespaceGeneration(),
		Types:        map[ids.ObjectAddress]*catalog.Type{base.Address(): base},
		Dependencies: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address()), Revision: base.Revision()}},
	}
	expected := core.Description{Temporals: original, Analysis: &analysis, PlanGeneration: auth.Session.DiscardPlansGeneration}
	retire := func(a core.AuthorizationView, d *core.Description) []analyze.TemporalConstant {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return core.EngineRetirePreparedInstalledScalarLiteralTemporalsLocked(e, a, d, path, d.Temporals)
	}
	if got := retire(auth, &expected); &got[0] != &original[0] {
		t.Fatal("live original type retired literal captures")
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Remove(e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address())); err != nil {
		t.Fatal(err)
	}
	auth.Catalog = e.StageRead(stage)
	got := retire(auth, &expected)
	if len(got) != 1 || !reflect.DeepEqual(got[0], original[2]) || &got[0] == &original[2] {
		t.Fatal("retirement must isolate candidate and retain unrelated temporal capture", got)
	}
	for _, tc := range []struct {
		name   string
		change func(*core.Description, *core.Analysis)
	}{
		{"missing source", func(_ *core.Description, a *core.Analysis) { a.Types = nil }},
		{"missing dependency", func(_ *core.Description, a *core.Analysis) { a.Dependencies = nil }},
		{"foreign dependency", func(_ *core.Description, a *core.Analysis) {
			a.Dependencies = []catalog.ExpressionDependency{{Ref: new(ids.Domain).Ref(auth.Catalog.Epoch(), base.Address())}}
		}},
		{"foreign source", func(d *core.Description, _ *core.Analysis) {
			d.Temporals[0].Value = sqltype.Null(foreign)
			d.Temporals[1].Value = sqltype.Null(foreign)
		}},
		{"wrong type id", func(d *core.Description, _ *core.Analysis) {
			d.Temporals[0].TypeID++
			d.Temporals[1].TypeID++
		}},
		{"epoch", func(_ *core.Description, a *core.Analysis) { a.Epoch++ }},
		{"discard", func(d *core.Description, _ *core.Analysis) { d.PlanGeneration++ }},
		{"retired analysis", func(_ *core.Description, a *core.Analysis) { a.NeedsReanalysis = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, d := analysis, expected
			d.Analysis = &a
			d.Temporals = append([]analyze.TemporalConstant(nil), original...)
			tc.change(&d, &a)
			snapshot := append([]analyze.TemporalConstant(nil), d.Temporals...)
			got := retire(auth, &d)
			if &got[0] != &d.Temporals[0] || !reflect.DeepEqual(got, snapshot) {
				t.Fatal("invalid authority retired or mutated captures")
			}
		})
	}
	if !reflect.DeepEqual(expected.Temporals, saved) || analysis.NeedsReanalysis {
		t.Fatal("candidate retirement mutated published statement state")
	}
}
