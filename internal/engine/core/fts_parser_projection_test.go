package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestFTSParserHandlerProjection(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	source := base.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1255})
	produce, ok := pgcatalog.ProducerFor(1255)
	if !ok {
		t.Fatal("missing actual pg_proc producer")
	}
	// Independent REL_18_6 pg_proc.dat literals, not the parser identity table.
	handlers := []struct {
		oid           ids.OID
		name          string
		arity, result int64
		arguments string
	}{
		{3717, "prsd_start", 2, 2281, "2281 23"},
		{3718, "prsd_nexttoken", 3, 2281, "2281 2281 2281"},
		{3719, "prsd_end", 1, 2278, "2281"},
		{3720, "prsd_headline", 3, 2281, "2281 2281 3615"},
		{3721, "prsd_lextype", 1, 2281, "2281"},
	}
	for _, h := range handlers {
		if _, represented := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: h.oid}); represented {
			t.Fatalf("baseline handler %d unexpectedly has a Procedure descriptor", h.oid)
		}
	}
	// This released adjacent identity is genuine but outside the authorized five.
	unrelated, found := catalog.BuiltinRoutineIdentityByOID(3710)
	if !found || unrelated.Name != "ts_rank_cd" {
		t.Fatal("missing independent unrelated identity witness", unrelated)
	}
	if _, represented := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3710}); represented {
		t.Fatal("unrelated witness is not lookup-only")
	}
	capture := func(t *testing.T, read catalog.Read, represented *catalog.Procedure) map[ids.OID][]sqltype.Value {
		t.Helper()
		rows, err := produce(pgcatalog.Input{Context: context.Background(), Catalog: read, Source: source, Owner: read.Owner(), Runtime: core.EngineCatalogRuntime{}})
		if err != nil {
			t.Fatal(err)
		}
		wanted := map[ids.OID]bool{3717: true, 3718: true, 3719: true, 3720: true, 3721: true}
		for _,oid:=range []ids.OID{40,41,54,55,2410,2411,2420,2421}{wanted[oid]=true}
		if represented != nil {
			delete(wanted, represented.Address().ObjectID)
		}
		seen := make(map[ids.OID]bool, len(rows))
		selected := make(map[ids.OID][]sqltype.Value, 5)
		supplements := 0
		for _, row := range rows {
			if len(row) != len(source.Columns) {
				t.Fatal("wrong real producer row width", len(row))
			}
			id, err := row[0].OID()
			if err != nil {
				t.Fatal(err)
			}
			oid := ids.OID(id)
			if seen[oid] {
				t.Fatalf("duplicate pg_proc row for %d", oid)
			}
			seen[oid] = true
			if _, exists := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}); !exists {
				if !wanted[oid] {
					t.Fatalf("unselected lookup-only identity %d supplemented", oid)
				}
				delete(wanted, oid)
				supplements++
			}
			for _, h := range handlers {
				if oid == h.oid {
					for i, value := range row {
						if value.Type() != source.Columns[i].Usage.Type {
							t.Fatal("foreign handler column descriptor", oid, i)
						}
					}
					selected[oid] = row
				}
			}
		}
		wantCount := 13
		if represented != nil {
			wantCount = 12
		}
		if supplements != wantCount || len(wanted) != 0 || len(selected) != 5 {
			t.Fatalf("supplement boundary: count=%d missing=%v selected=%d", supplements, wanted, len(selected))
		}
		if seen[3710] {
			t.Fatal("unrelated lookup-only ts_rank_cd became a pg_proc row")
		}
		return selected
	}
	baseline := capture(t, base, nil)
	for _, h := range handlers {
		want := []any{int64(h.oid), h.name, int64(11), int64(source.Owner.ID()), h.arity, h.result, "i", true, "s", "f", nil, int64(12), false, float64(0), h.name, h.arguments}
		if got := parserProjectionCells(t, baseline[h.oid]); !reflect.DeepEqual(got, want) {
			t.Fatalf("released handler %d: got=%#v want=%#v", h.oid, got, want)
		}
	}
	for _, h := range handlers {
		t.Run(fmt.Sprint(h.oid), func(t *testing.T) {
			// Install an ordinary metadata descriptor into a private real catalog
			// state. Its current name/signature/result/attributes intentionally
			// differ from the released supplement; no SQL callback is installed.
			represented := &catalog.Procedure{
				ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: h.oid},
				ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
				ObjectRevision:  1, ObjectName: "represented_parser_handler", Signature: "25",
				RoutineKind: catalog.FunctionRoutine, Owner: source.Owner, Result: 25,
				Volatility: expr.Volatile, Strict: false,
				Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: h.name},
			}
			state := catalog.CloneState(base.State())
			state.SetDescriptor(represented.Address(), represented)
			state.BindName(catalog.DescriptorNameKey(represented), represented.Address())
			rows := capture(t, base.WithState(state), represented)
			want := []any{int64(h.oid), "represented_parser_handler", int64(11), int64(source.Owner.ID()), int64(1), int64(25), "v", false, "s", "f", nil, int64(12), false, float64(0), h.name, "25"}
			if got := parserProjectionCells(t, rows[h.oid]); !reflect.DeepEqual(got, want) {
				t.Fatalf("represented descriptor lost precedence: got=%#v want=%#v", got, want)
			}
			for _, other := range handlers {
				if other.oid != h.oid && !reflect.DeepEqual(parserProjectionCells(t, rows[other.oid]), parserProjectionCells(t, baseline[other.oid])) {
					t.Fatal("represented sibling changed another supplement", other.oid)
				}
			}
		})
	}
}

func parserProjectionCells(t *testing.T, row []sqltype.Value) []any {
	t.Helper()
	cells := make([]any, len(row))
	for i, value := range row {
		if value.IsNull() {
			continue
		}
		var err error
		switch value.Type().ID() {
		case sqltype.Oid:
			var oid uint32
			oid, err = value.OID()
			cells[i] = int64(oid)
		case sqltype.Int2, sqltype.Int4, sqltype.Int8:
			cells[i], err = value.Integer()
		case sqltype.Bool:
			cells[i], err = value.Boolean()
		case sqltype.Float4:
			cells[i], err = value.Float()
		case sqltype.Name, sqltype.Char, sqltype.Text:
			cells[i], err = value.Text()
		case sqltype.OidVector:
			cells[i],err=value.Type().Output(context.Background(),value)
		default:
			t.Fatal("unexpected non-NULL handler metadata carrier", i, value.Type().ID())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return cells
}
