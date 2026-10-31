package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRegclassNumericClassification(t *testing.T) {
	typ, _ := engine.New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		input   string
		numeric bool
		oid     uint32
		code    string
	}{
		{"-", true, 0, ""}, {"0", true, 0, ""}, {"010", true, 8, ""}, {"4294967295", true, 4294967295, ""},
		{"08", true, 0, "22P02"}, {"4294967296", true, 0, "22003"}, {"", false, 0, ""}, {"+1", false, 0, ""}, {"-1", false, 0, ""}, {" 42 ", false, 0, ""}, {"a.b", false, 0, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			oid, numeric, err := regobj.ClassNumeric(tc.input, typ)
			if numeric != tc.numeric || oid != tc.oid {
				t.Fatal(oid, numeric, err)
			}
			var d *sqlstate.Error
			if tc.code != "" {
				if !errors.As(err, &d) || d.Code != tc.code {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegclassScalarDependencyAndReplaySeparation(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Regclass)
	arrayTyp, _ := view.LookupType(typ.AssociatedArray())
	value := sqltype.String(typ, "1259")
	var err error
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
	var deps []ids.ObjectRef
	if _, err = (stored.ScalarRenderer{Dependencies: &deps}).SQL(context.Background(), view, literal); err != nil || len(deps) != 2 || deps[0].Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}) || deps[1].Address != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}) {
		t.Fatal(deps, err)
	}
	array, err := sqltype.OwnedArrayValue(arrayTyp, []sqltype.ArrayDimension{{Length: 1, Lower: 1}}, []sqltype.Value{value})
	if err != nil {
		t.Fatal(err)
	}
	opaque := plan.LiteralExpr{Info: expr.Info{Usage: arrayTyp.Usage()}, Value: array}
	deps = nil
	if _, err = (stored.ScalarRenderer{Dependencies: &deps}).SQL(context.Background(), view, opaque); err != nil {
		t.Fatal(err)
	}
	for _, dep := range deps {
		if dep.Address.ClassID == ids.RelationClassID {
			t.Fatal("opaque array acquired DROP dependency", dep)
		}
	}
	var replay []ids.ObjectAddress
	if err = regobj.ClassReplayReferences(view, opaque, func(a ids.ObjectAddress) error { replay = append(replay, a); return nil }); err != nil || !reflect.DeepEqual(replay, []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: 1259}}) {
		t.Fatal(replay, err)
	}
	sentinel := errors.New("replay sentinel")
	if err = regobj.ClassReplayReferences(view, opaque, func(ids.ObjectAddress) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	intTyp, _ := view.LookupType(sqltype.Int4)
	integer, err := sqltype.Integer(intTyp, 1259)
	if err != nil {
		t.Fatal(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(intTyp.Usage(), typ.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	numeric := plan.CoercionExpr{Info: expr.Info{Usage: typ.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: intTyp.Usage()}, Value: integer}, Plan: coercionPlan, Mode: sqltype.Explicit}
	replay = nil
	if err = regobj.ClassReplayReferences(view, numeric, func(a ids.ObjectAddress) error { replay = append(replay, a); return nil }); err != nil || len(replay) != 0 {
		t.Fatal(replay, err)
	}
	if !table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: numeric}) {
		t.Fatal("closed numeric regclass default not deferred")
	}
}
