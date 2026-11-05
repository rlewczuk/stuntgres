package schemacmds_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
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
)

func TestIntarraySortProviderIdentity(t *testing.T) {
	e, ext, _ := intarrayUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.Members) != 46 || len(ext.Installation.Members) != 44 || len(ext.Installation.Declarations) != 44 {
		t.Fatal("closed sort recipe")
	}
	prefix := []string{"query_int", "bqarr_in", "bqarr_out", "intbig_gkey", "_intbig_in", "_intbig_out", "_int_overlap", "_int_contains", "_int_contained", "intarray_push_elem", "intarray_push_array", "intarray_del_elem", "intset_subtract", "intset_union_elem", "_int_union", "_int_inter", "_int_overlap_sel", "_int_contains_sel", "_int_contained_sel", "_int_overlap_joinsel", "_int_contains_joinsel", "_int_contained_joinsel", "&&", "@>", "<@", "+", "+", "-", "-", "|", "|", "&", "icount", "idx", "subarray", "subarray", "intset", "#", "#"}
	for slot, name := range prefix {
		d := view.Get(ext.Installation.Members[slot])
		class := ids.ProcedureClassID
		if slot == 0 || slot == 3 {
			class = ids.TypeClassID
		}
		if slot >= 22 && slot < 32 || slot >= 37 && slot < 39 {
			class = ids.OperatorClassID
		}
		if d.Name() != name || d.Address().ClassID != class {
			t.Fatal("preserved prefix changed", slot, d)
		}
	}
	members := slices.Clone(ext.Members)
	for i, tc := range []struct {
		name      string
		signature catalog.ProcedureSignature
	}{
		{"sort", "1007"}, {"sort", "1007,25"}, {"sort_asc", "1007"}, {"sort_desc", "1007"}, {"uniq", "1007"},
	} {
		slot := 39 + i
		proc := view.MustProcedure(ext.Installation.Members[slot])
		operation, ok := extension.IntarraySortProcedure(view, proc)
		if !ok || operation != sqltype.IntarraySortOperation(i) || proc.Name() != tc.name || proc.Signature != tc.signature || proc.Result != 1007 || !proc.Strict || proc.Volatility != expr.Immutable || proc.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: tc.name, Binary: "$libdir/_int", HasBinary: true}) || proc.Origin.Installation != ext.Installation || proc.Origin.Declaration != slot {
			t.Fatal("exact sort declaration", slot, proc)
		}
		if _, ok := extension.IntarraySetProcedure(view, proc); ok {
			t.Fatal("sort admitted as SET")
		}
		if _, ok := extension.IntarraySliceProcedure(view, proc); ok {
			t.Fatal("sort admitted as SLICE")
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Signature = "23" }, func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Volatility = expr.Volatile },
			func(p *catalog.Procedure) {
				p.Display.Source = "sort_asc"
				if tc.name == "sort_asc" {
					p.Display.Source = "sort"
				}
			},
			func(p *catalog.Procedure) { p.Display.Binary = "$libdir/foreign" }, func(p *catalog.Procedure) { p.Display.Language = catalog.SQLRoutineLanguage },
			func(p *catalog.Procedure) { p.Implementation = extension.StockSeg },
			func(p *catalog.Procedure) { o := *p.Origin; o.Declaration = 37; p.Origin = &o },
			func(p *catalog.Procedure) { o := *p.Origin; o.Declaration = 44; p.Origin = &o },
			func(p *catalog.Procedure) {
				o := *p.Origin
				copied := *o.Installation
				o.Installation = &copied
				p.Origin = &o
			},
			func(p *catalog.Procedure) { p.Owner = new(authorization.Domain).Principal(p.Owner.ID()) },
		} {
			bad := *proc
			mutate(&bad)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(bad.Address(), &bad)
			current := e.Catalog.Read(state)
			if _, err := extension.ProcedureSymbol(current, &bad); err == nil {
				t.Fatal("forged sort declaration admitted", slot)
			}
			if _, ok := extension.IntarraySortProcedure(current, &bad); ok {
				t.Fatal("forged role executable", slot)
			}
			if err := extension.StockIntarray.ValidateProcedure(view, proc); err != nil {
				t.Fatal("negative candidate changed source", err)
			}
		}
	}
	if !slices.Equal(members, ext.Members) {
		t.Fatal("validation mutated accepted manifest")
	}
	t.Run("presentation", func(t *testing.T) {
		target := engine.New()
		op, done := enginetest.RoleOperation(t, target, "postgres")
		defer done()
		enginetest.RoleExecute(t, target, op, "CREATE EXTENSION intarray")
		before := target.CatalogRead()
		installed := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
		enginetest.RoleExecute(t, target, op, "ALTER FUNCTION sort(integer[],text) RENAME TO selected_direction")
		enginetest.RoleExecute(t, target, op, "CREATE SCHEMA moved_sort")
		enginetest.RoleExecute(t, target, op, "ALTER EXTENSION intarray SET SCHEMA moved_sort")
		current := target.CatalogRead()
		proc := current.MustProcedure(installed.Members[40])
		if operation, ok := extension.IntarraySortProcedure(current, proc); !ok || operation != sqltype.IntarraySortDirection || proc.Name() != "selected_direction" {
			t.Fatal("presentation changed original sort role")
		}
	})
	t.Run("late_collision", func(t *testing.T) {
		target := engine.New()
		op, done := enginetest.RoleOperation(t, target, "postgres")
		defer done()
		enginetest.RoleExecute(t, target, op, "CREATE FUNCTION uniq(integer[]) RETURNS integer[] LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT $1'")
		before := target.Catalog.State
		target.Mu.Lock()
		stage, err := schemacmds.StageExtensionLocked(target, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "intarray"}})
		target.Mu.Unlock()
		extensionUnitDiscardFailed(t, target, stage)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || target.Catalog.State != before {
			t.Fatal("late sort collision published prefix", err)
		}
	})
}
