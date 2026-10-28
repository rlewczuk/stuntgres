package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRoutineDefaultPresentation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	runtime := core.EngineCatalogRuntime{Auth: dtpUnitAuthority(t, f, view)}
	for _, x := range []struct {
		oid  ids.OID
		slot int
		want string
	}{
		{3464, 0, "0"}, {3464, 6, "0.0"}, {6212, 0, "0"}, {6212, 1, "1"}, {1268, 1, "true"}, {4005, 2, "'{}'::jsonb"}, {4005, 3, "false"}, {4350, 1, "'NFC'::text"},
		{3782, 3, "'{}'::text[]"}, {3783, 3, "'{}'::text[]"},
		{3784, 3, "'{}'::text[]"}, {3785, 3, "'{}'::text[]"},
	} {
		d := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: x.oid})
		got, err := runtime.RoutineDefault(t.Context(), view, d, x.slot)
		if err != nil || got != x.want {
			t.Fatal(x, got, err)
		}
		copied := *d
		if _, err := runtime.RoutineDefault(t.Context(), view, &copied, x.slot); err == nil {
			t.Fatal("copied declaration accepted")
		}
	}
	d := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1268})
	if _, err := runtime.RoutineDefault(t.Context(), engine.New().CatalogRead(), d, 1); err == nil {
		t.Fatal("foreign catalog accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runtime.RoutineDefault(canceled, view, d, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, recipe := range []*catalog.RoutineLiteralDefaults{
		nil, catalog.NewRoutineLiteralDefaults(), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 99999, Text: "1"}), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 16, Text: "bad"}), catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 16, Null: true, Text: "bad"}),
	} {
		state := catalog.CloneState(view.State())
		copy := *d
		copy.LiteralDefaults = recipe
		state.SetDescriptor(copy.Address(), &copy)
		captured := f.e.Catalog.Read(state)
		r := core.EngineCatalogRuntime{Auth: dtpUnitAuthority(t, f, captured)}
		if _, err := r.RoutineDefault(t.Context(), captured, &copy, 1); err == nil {
			t.Fatal("malformed recipe accepted", recipe)
		}
	}
	// Real checked SQL default authority uses the presentation deparser, while its
	// independent replay renderer and captured node remain unchanged.
	state := catalog.CloneState(view.State())
	integer, _ := view.LookupType(sqltype.Int4)
	value, _ := sqltype.Integer(integer, 9)
	definition := &proc.Definition{}
	if err := definition.SetFormals([]proc.Formal{{Name: "a", Mode: "i", Usage: integer.Usage(), HasDefault: true}}); err != nil {
		t.Fatal(err)
	}
	sqlRoutine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Signature: "23", SQL: definition}
	state.SetDescriptor(sqlRoutine.Address(), sqlRoutine)
	captured := f.e.Catalog.Read(state)
	node := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}
	defaults, err := stored.NewRoutineDefaults(t.Context(), captured, nil, []expr.Expr{node})
	if err != nil {
		t.Fatal(err)
	}
	sqlRoutine.SQLDefaults = defaults
	r := core.EngineCatalogRuntime{Auth: dtpUnitAuthority(t, f, captured)}
	replay, err := defaults.DefaultSQL(t.Context(), captured, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.RoutineDefault(t.Context(), captured, sqlRoutine, 0)
	if err != nil || got != "9" {
		t.Fatal(got, err)
	}
	if again, err := defaults.DefaultSQL(t.Context(), captured, 0); err != nil || again != replay {
		t.Fatal("replay changed", again, err)
	}
	if _, err := r.RoutineDefault(t.Context(), captured, sqlRoutine, 1); err == nil {
		t.Fatal("absent input default accepted")
	}
	null := catalog.NewRoutineLiteralDefaults(catalog.RoutineLiteral{SourceType: 20, Null: true})
	literalRoutine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48002}, Signature: "20", Installed: catalog.InstalledRoutineDeclaration{Defaults: 1}, LiteralDefaults: null}
	next := catalog.CloneState(state)
	next.SetDescriptor(literalRoutine.Address(), literalRoutine)
	nv := f.e.Catalog.Read(next)
	nr := core.EngineCatalogRuntime{Auth: dtpUnitAuthority(t, f, nv)}
	if got, err := nr.RoutineDefault(t.Context(), nv, literalRoutine, 0); err != nil || got != "NULL::bigint" {
		t.Fatal("typed NULL", got, err)
	}
}
