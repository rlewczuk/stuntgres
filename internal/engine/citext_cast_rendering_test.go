package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Public lifecycle coverage uses a real renamed-schema default and replay.
// This unit also exercises carrier/manifest corruption that SQL cannot forge.
func TestCitextCastRoutineRenderingAuthority(t *testing.T) {
	e, installation, base := citextUnitInstalled(t)
	view := e.CatalogRead()
	procedure := view.MustProcedure(installation.Members[extension.CitextConversionProcedureStart+1]) // Real citext(boolean).
	boolean, ok := view.LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing boolean descriptor")
	}
	argument := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	call, err := functions.NewCastRoutine(expr.Info{Usage: base.SQLType.Usage(), RoutineReference: procedure}, procedure.Name(), "public", []expr.Expr{argument}, procedure.Volatility)
	if err != nil {
		t.Fatal(err)
	}
	state := catalog.CloneState(view.State())
	namespace, ok := view.Namespace(procedure.Namespace())
	if !ok {
		t.Fatal("missing installation namespace")
	}
	moved := *namespace
	moved.ObjectName, moved.ObjectRevision = "cast_rendering_moved", namespace.Revision()+1
	state.UnbindName(catalog.DescriptorNameKey(namespace))
	state.SetDescriptor(moved.Address(), &moved)
	state.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	current := e.catalog.Read(state)
	text, err := stored.DefaultSQL(context.Background(), current, call)
	if err != nil || !strings.HasPrefix(text, `"cast_rendering_moved"."citext"(`) {
		t.Fatal("rendering did not project current checked namespace", text, err)
	}
	if call.RoutineReference != procedure || call.Namespace != "public" || procedure != view.MustProcedure(procedure.Address()) || namespace.Name() != "public" {
		t.Fatal("rendering refreshed or mutated original capture")
	}
	if original, err := stored.DefaultSQL(context.Background(), view, call); err != nil || !strings.HasPrefix(original, `"public"."citext"(`) {
		t.Fatal("old snapshot no longer renders its original identity", original, err)
	}
	t.Run("foreign_carrier", func(t *testing.T) {
		foreign, ext, _ := citextUnitInstalled(t)
		grafted := call
		grafted.RoutineReference = foreign.CatalogRead().MustProcedure(ext.Members[extension.CitextConversionProcedureStart+1])
		if _, err := stored.DefaultSQL(context.Background(), current, grafted); err == nil {
			t.Fatal("foreign installed routine carrier accepted")
		}
	})
	t.Run("stale_declaration", func(t *testing.T) {
		changed := *procedure
		changed.Volatility = expr.Stable
		grafted := call
		grafted.RoutineReference = &changed
		if _, err := stored.DefaultSQL(context.Background(), current, grafted); err == nil {
			t.Fatal("changed immutable declaration carrier accepted")
		}
	})
	t.Run("invalid_provider_manifest", func(t *testing.T) {
		broken := catalog.CloneState(state)
		ext := *installation
		ext.Members = append([]ids.ObjectAddress(nil), installation.Members...)
		ext.Members[extension.CitextConversionProcedureStart+1] = ext.Members[extension.CitextConversionProcedureStart]
		broken.SetDescriptor(ext.Address(), &ext)
		if _, err := stored.DefaultSQL(context.Background(), e.catalog.Read(broken), call); err == nil {
			t.Fatal("cast routine outside its provider manifest accepted")
		}
	})
}
