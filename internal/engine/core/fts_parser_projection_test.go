package core_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
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
		arguments     string
	}{
		{3717, "prsd_start", 2, 2281, "2281 23"},
		{3718, "prsd_nexttoken", 3, 2281, "2281 2281 2281"},
		{3719, "prsd_end", 1, 2278, "2281"},
		{3720, "prsd_headline", 3, 2281, "2281 2281 3615"},
		{3721, "prsd_lextype", 1, 2281, "2281"},
	}
	for _, h := range handlers {
		d, represented := base.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: h.oid})
		if !represented || d.Builtin == nil || d.Builtin != routine.BuiltinRoutine(h.oid) {
			t.Fatalf("handler %d lacks its canonical metadata descriptor", h.oid)
		}
		identity := catalog.RoutineIdentity{ID:h.oid, Namespace:11, Name:h.name, Signature:catalog.ProcedureSignature(strings.ReplaceAll(h.arguments," ",","))}
		if d.Builtin.Identity != identity || d.Builtin.Providers != 0 || d.Builtin.Result != ids.OID(h.result) || d.RoutineKind != catalog.FunctionRoutine || d.Result != ids.OID(h.result) || d.Name() != h.name || d.Namespace().ObjectID != 11 || d.Owner != source.Owner || !d.Strict || d.Volatility != expr.Immutable || d.Builtin.Parallel != 's' || d.ReturnsSet() || d.CostEstimate() != 1 {
			t.Fatalf("handler %d lost released non-executable metadata: %+v", h.oid, d)
		}
		if d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Foreign != nil || d.Implementation != nil || d.PLHandler != catalog.NoPLHandler || d.SecurityDefiner || d.Configuration != nil || d.Display != (catalog.RoutineDisplay{Language:catalog.InternalRoutineLanguage,Source:h.name}) {
			t.Fatalf("handler %d acquired SQL/foreign/executable authority or changed display",h.oid)
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
	capture := func(t *testing.T, read catalog.Read) map[ids.OID][]sqltype.Value {
		t.Helper()
		rows, err := produce(pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: e.RoleStore.PublishedRead(read.Owner(), read), Source: source, Owner: read.Owner(), Runtime: core.EngineCatalogRuntime{}})
		if err != nil {
			t.Fatal(err)
		}
		wanted := map[ids.OID]bool{3717: true, 3718: true, 3719: true, 3720: true, 3721: true}
		for _, oid := range []ids.OID{40, 41, 54, 55, 2410, 2411, 2420, 2421, 88, 1644, 1645, 1646, 1647, 1648, 1649, 1650, 1651, 1652, 1653, 1654, 1655} {
			wanted[oid] = true
		}
		seen := make(map[ids.OID]bool, len(rows))
		selected := make(map[ids.OID][]sqltype.Value, 5)
		metadataRows := 0
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
				t.Fatalf("pg_proc row %d has no owning Procedure descriptor",oid)
			}
			if wanted[oid] {
				delete(wanted,oid)
				metadataRows++
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
		if metadataRows != 26 || len(wanted) != 0 || len(selected) != 5 {
			t.Fatalf("metadata descriptor boundary: count=%d missing=%v selected=%d", metadataRows, wanted, len(selected))
		}
		if seen[3710] {
			t.Fatal("unrelated lookup-only ts_rank_cd became a pg_proc row")
		}
		return selected
	}
	baseline := capture(t, base)
	for _, h := range handlers {
		want := []any{int64(h.oid), h.name, int64(11), int64(source.Owner.ID()), int64(12), float64(1), float64(0), int64(0), int64(0), "f", false, false, true, false, "i", "s", h.arity, int64(0), h.result, h.arguments, nil, nil, nil, nil, nil, h.name, nil, nil, nil, nil}
		if got := parserProjectionCells(t, baseline[h.oid]); !reflect.DeepEqual(got, want) {
			t.Fatalf("released handler %d: got=%#v want=%#v", h.oid, got, want)
		}
	}
	for _, h := range handlers {
		t.Run(fmt.Sprint(h.oid), func(t *testing.T) {
			// Replace an ordinary metadata descriptor in a private real catalog
			// state. Its current name/signature/result/attributes intentionally
			// differ from the released declaration; no SQL callback is installed.
			represented := &catalog.Procedure{
				ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: h.oid},
				ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
				ObjectRevision:  1, ObjectName: "represented_parser_handler", Signature: "25",
				RoutineKind: catalog.FunctionRoutine, Owner: source.Owner, Result: 25,
				Volatility: expr.Volatile, Strict: false, Parallel: 's', Cost: 1,
				Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: h.name},
			}
			state := catalog.CloneState(base.State())
			state.SetDescriptor(represented.Address(), represented)
			state.BindName(catalog.DescriptorNameKey(represented), represented.Address())
			rows := capture(t, base.WithState(state))
			want := []any{int64(h.oid), "represented_parser_handler", int64(11), int64(source.Owner.ID()), int64(12), float64(1), float64(0), int64(0), int64(0), "f", false, false, false, false, "v", "s", int64(1), int64(0), int64(25), "25", nil, nil, nil, nil, nil, h.name, nil, nil, nil, nil}
			if got := parserProjectionCells(t, rows[h.oid]); !reflect.DeepEqual(got, want) {
				t.Fatalf("current descriptor lost precedence: got=%#v want=%#v", got, want)
			}
			for _, other := range handlers {
				if other.oid != h.oid && !reflect.DeepEqual(parserProjectionCells(t, rows[other.oid]), parserProjectionCells(t, baseline[other.oid])) {
					t.Fatal("replacing one descriptor changed another handler", other.oid)
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
		case sqltype.Regproc:
			var oid uint32
			oid, err = value.ProcedureOID()
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
			cells[i], err = value.Type().Output(context.Background(), value)
		default:
			t.Fatal("unexpected non-NULL handler metadata carrier", i, value.Type().ID())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return cells
}
