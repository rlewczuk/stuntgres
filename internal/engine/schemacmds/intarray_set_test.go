package schemacmds_test

import (
	"errors"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntarraySetProviderIdentity(t *testing.T) {
	e, ext, _ := intarrayUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.Members) != 34 || len(ext.Installation.Members) != 32 || len(ext.Installation.Declarations) != 32 {
		t.Fatal("selected recipe boundaries")
	}
	names := []string{"_int_overlap", "_int_contains", "_int_contained", "intarray_push_elem", "intarray_push_array", "intarray_del_elem", "intset_subtract", "intset_union_elem", "_int_union", "_int_inter", "_int_overlap_sel", "_int_contains_sel", "_int_contained_sel", "_int_overlap_joinsel", "_int_contains_joinsel", "_int_contained_joinsel"}
	for slot, address := range ext.Installation.Members {
		class := ids.ProcedureClassID
		if slot == 0 || slot == 3 {
			class = ids.TypeClassID
		}
		if slot >= 22 {
			class = ids.OperatorClassID
		}
		if address.ClassID != class {
			t.Fatal("wrong member class", slot, address)
		}
	}
	for i, name := range names {
		slot := i + 6
		proc := view.MustProcedure(ext.Installation.Members[slot])
		symbol, err := extension.ProcedureSymbol(view, proc)
		volatility := expr.Immutable
		if slot >= 16 {
			volatility = expr.Stable
		}
		if err != nil || symbol != name || proc.Display.Source != name || proc.Display.Binary != "$libdir/_int" || !proc.Strict || proc.Volatility != volatility || proc.Installed.Parallel != 0 || proc.Origin.Installation != ext.Installation || proc.Origin.Declaration != slot {
			t.Fatal("exact declaration", slot, proc, err)
		}
		operation, executable := extension.IntarraySetProcedure(view, proc)
		if executable != (slot < 16) || executable && int(operation) != i {
			t.Fatal("estimator acquired executable role", slot, operation)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Volatility = expr.Volatile },
			func(p *catalog.Procedure) { p.Signature = "1007,25" }, func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) {
				p.Display.Source = "_int_overlap"
				if i == 0 {
					p.Display.Source = "_int_contains"
				}
			},
			func(p *catalog.Procedure) {
				o := *p.Origin
				o.Declaration = 16
				if slot == 16 {
					o.Declaration = 6
				}
				p.Origin = &o
			},
			func(p *catalog.Procedure) {
				o := *p.Origin
				copy := *o.Installation
				o.Installation = &copy
				p.Origin = &o
			},
			func(p *catalog.Procedure) { p.Implementation = extension.StockSeg },
		} {
			candidate := catalog.CloneState(view.State())
			bad := *proc
			mutate(&bad)
			candidate.SetDescriptor(bad.Address(), &bad)
			if _, err := extension.ProcedureSymbol(e.Catalog.Read(candidate), &bad); err == nil {
				t.Fatal("forged selected authority", slot)
			}
			if got, err := extension.ProcedureSymbol(view, proc); err != nil || got != name {
				t.Fatal("negative validation changed accepted origin", err)
			}
		}
	}
	original := slices.Clone(ext.Members)
	for i := range 10 {
		address := ext.Installation.Members[22+i]
		before := view.MustOperator(address)
		for _, mutate := range []func(*catalog.Operator){func(o *catalog.Operator) { o.Function++ }, func(o *catalog.Operator) { o.Commutator++ }, func(o *catalog.Operator) { o.Restriction++ }, func(o *catalog.Operator) { o.Join++ }, func(o *catalog.Operator) { o.Hash = true }} {
			state := catalog.CloneState(view.State())
			bad := *before
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockIntarray.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("invalid operator accepted", i)
			}
		}
	}
	if !slices.Equal(original, ext.Members) {
		t.Fatal("validation mutated installed graph")
	}
	t.Run("late_selected_collision", func(t *testing.T) {
		target := engine.New()
		op, done := enginetest.RoleOperation(t, target, "postgres")
		defer done()
		enginetest.RoleExecute(t, target, op, "CREATE FUNCTION _int_inter(integer[],integer[]) RETURNS integer[] LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT $1'")
		before := target.Catalog.State
		target.Mu.Lock()
		stage, err := schemacmds.StageExtensionLocked(target, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "intarray"}})
		target.Mu.Unlock()
		extensionUnitDiscardFailed(t, target, stage)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || diagnostic.Message != `function "_int_inter" already exists with same argument types` || target.Catalog.State != before {
			t.Fatal("late collision leaked private graph", err)
		}
	})
	// Genuine presentation/membership changes do not select a different operation.
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	proc := view.MustProcedure(ext.Installation.Members[6])
	enginetest.RoleExecute(t, e, op, "ALTER FUNCTION _int_overlap(integer[],integer[]) RENAME TO selected_overlap")
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION intarray DROP FUNCTION selected_overlap(integer[],integer[])")
	current := e.CatalogRead().MustProcedure(proc.Address())
	if operation, ok := extension.IntarraySetProcedure(e.CatalogRead(), current); !ok || operation != sqltype.IntarrayOverlap || current.Origin.Installation != proc.Origin.Installation {
		t.Fatal("lawful detached identity lost")
	}
}
