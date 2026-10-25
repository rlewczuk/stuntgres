package core_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryTableCreateRestoreColumnAuthority(t *testing.T) {
	for _, mode := range []string{"raw", "forged", "isolated"} {
		e := engine.New()
		op, done, err := enginetest.Operation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(done)
		if mode != "raw" {
			core.OperationReplay(op).TableDestination = true
		}
		if mode == "isolated" {
			e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
		}
		before := e.Catalog.State
		for _, raw := range []parser.TypeName{{Name: "char", Quoted: true}, {Name: "regclass"}, {Name: "regclass", ArrayBounds: []int32{0}}, {Name: "xid"}, {Name: "xid", Schema: &parser.Identifier{Text: "pg_catalog"}}, {Name: "cid"}, {Name: "xid8"}} {
			e.Mu.Lock()
			usage, err := core.EngineResolveTableColumnType(e, raw, op)
			e.Mu.Unlock()
			if mode == "forged" {
				if err == nil || usage.Type != nil || e.Catalog.State != before {
					t.Fatal("forged marker admitted a type or changed catalog state", raw, usage, err)
				}
				continue
			}
			if mode == "isolated" || raw.Name != "char" {
				if err != nil {
					t.Fatal(mode, raw, err)
				}
				id := sqltype.Regclass
				if raw.Name == "char" {
					id = sqltype.Char
				} else if raw.Name == "xid" {
					id = sqltype.Xid
				} else if raw.Name == "cid" {
					id = sqltype.Cid
				} else if raw.Name == "xid8" {
					id = sqltype.Xid8
				}
				if len(raw.ArrayBounds) == 0 && usage.Type != enginetest.Usage(e, id).Type || len(raw.ArrayBounds) != 0 && (!usage.Type.IsArray() || usage.Type.Element() != enginetest.Usage(e, id).Type) {
					t.Fatal(mode, raw, usage)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
					t.Fatal(mode, raw, err)
				}
			}
		}
		// The private caller never changes ordinary cast or domain-base admission.
		e.Mu.Lock()
		_, castErr := core.EngineResolveAnnotationType(e, parser.TypeName{Name: "char", Quoted: true}, op, false)
		_, domainErr := core.EngineResolveAnnotationTypeMode(e, parser.TypeName{Name: "char", Quoted: true}, op, false, true)
		arrayUsage, arrayErr := core.EngineResolveTableColumnType(e, parser.TypeName{Name: "char", Quoted: true, ArrayBounds: []int32{0}}, op)
		_, xidCastErr := core.EngineResolveAnnotationType(e, parser.TypeName{Name: "xid"}, op, false)
		_, xidDomainErr := core.EngineResolveAnnotationTypeMode(e, parser.TypeName{Name: "xid"}, op, false, true)
		_, xidArrayErr := core.EngineResolveTableColumnType(e, parser.TypeName{Name: "xid", ArrayBounds: []int32{0}}, op)
		_, xidModifierErr := core.EngineResolveTableColumnType(e, parser.TypeName{Name: "xid", Modifiers: []parser.TypeModifier{{Integer: new(int32)}}}, op)
		e.Mu.Unlock()
		if mode == "forged" {
			for _, err := range []error{castErr, domainErr, arrayErr, xidCastErr, xidDomainErr, xidArrayErr, xidModifierErr} {
				if err == nil {
					t.Fatal("forged marker admitted ordinary type resolution")
				}
			}
			if e.Catalog.State != before {
				t.Fatal("forged marker changed catalog state")
			}
			continue
		}
		if xidCastErr != nil || xidDomainErr != nil {
			t.Fatal(mode, xidCastErr, xidDomainErr)
		}
		if arrayErr != nil || !arrayUsage.Type.IsArray() || arrayUsage.Type.Element() != enginetest.Usage(e, sqltype.Char).Type {
			t.Fatal(mode, "internal char array storage", arrayUsage, arrayErr)
		}
		var modifier *sqlstate.Error
		if !errors.As(xidModifierErr, &modifier) || modifier.Code != "42601" || modifier.Message != `type modifier is not allowed for type "xid"` {
			t.Fatal(mode, xidModifierErr)
		}
		for _, err := range []error{castErr, domainErr, xidArrayErr} {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
				t.Fatal(mode, err)
			}
		}
	}
}

func TestQueryTableCreateRegclassArrayReownership(t *testing.T) {
	source := engine.New()
	anchorStage, anchor := queryCreateStage(t, source, "anchor", 0)
	queryCreatePublish(t, source, anchorStage)
	regclass := enginetest.Usage(source, sqltype.Regclass).Type
	array, ok := source.CatalogRead().LookupArray(regclass)
	if !ok {
		t.Fatal("existing REGCLASS array descriptor is missing")
	}
	before := strconv.FormatUint(uint64(anchor.Address().ObjectID), 10)
	value, err := array.Input(context.Background(), "[0:1]={"+before+",NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	target.Catalog.InstallOIDFrontierLocked(anchor.Address().ObjectID + 100)
	targetAnchorStage, targetAnchor := queryCreateStage(t, target, "anchor", 0)
	queryCreatePublish(t, target, targetAnchorStage)
	targetArray, ok := target.CatalogRead().LookupArray(enginetest.Usage(target, sqltype.Regclass).Type)
	if !ok {
		t.Fatal("replacement REGCLASS array descriptor is missing")
	}
	sourceStage, sourceCopy := queryCreateStage(t, source, "copy", 1)
	sourceCopy.Columns[0].Usage = array.Usage()
	if err = source.StageQueryTableRows(context.Background(), sourceStage, sourceCopy, [][]sqltype.Value{{value}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, source, sourceStage)
	stage, d := queryCreateStage(t, target, "copy", 1)
	d.Columns[0].Usage = targetArray.Usage()
	queryCreatePublish(t, target, stage)
	candidate := catalog.CloneState(target.CatalogRead().State())
	imported := *d
	imported.Columns = append([]catalog.Column(nil), d.Columns...)
	imported.Columns[0].Usage = array.Usage()
	candidate.SetDescriptor(d.Address(), &imported)
	stores := map[ids.ObjectAddress]*storage.Relation{d.Address(): source.Storage.Relations[sourceCopy.Address()]}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{anchor.Address(): targetAnchor.Address()}
	if err = stored.ReownScalarTypes(candidate, stores, mapping); err != nil {
		t.Fatal(err)
	}
	row := stores[d.Address()].Versions[0].Values[0]
	if row.Type() != targetArray || row.ArrayLen() != 2 || !row.ArrayElement(1).IsNull() || !reflect.DeepEqual(row.ArrayDimensions(), []sqltype.ArrayDimension{{Lower: 0, Length: 2}}) {
		t.Fatal(row.Type(), row.ArrayDimensions())
	}
	got, err := row.ArrayElement(0).Text()
	want := strconv.FormatUint(uint64(targetAnchor.Address().ObjectID), 10)
	if err != nil || got != want || got == before {
		t.Fatal("foreign relation identity retained", got, want, err)
	}
	old, err := value.ArrayElement(0).Text()
	if err != nil || old != before {
		t.Fatal("retained original array mutated", old, err)
	}
	if candidate.MustRelation(d.Address()).Columns[0].Usage.Type != targetArray {
		t.Fatal("stored Usage was not reowned")
	}
}

func TestQueryTableCreateXIDScalarReownership(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Xid, sqltype.Cid, sqltype.Xid8} {
		identifierScalarReownership(t, id)
	}
}

func identifierScalarReownership(t *testing.T, id sqltype.ID) {
	t.Helper()
	max := "4294967295"
	if id == sqltype.Xid8 {
		max = "18446744073709551615"
	}
	source, target := engine.New(), engine.New()
	sourceType := enginetest.Usage(source, id).Type
	targetType := enginetest.Usage(target, id).Type
	var rows [][]sqltype.Value
	for _, text := range []string{"0", max} {
		value, err := sourceType.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, []sqltype.Value{value})
	}
	rows = append(rows, []sqltype.Value{sqltype.Null(sourceType)})
	sourceStage, sourceCopy := queryCreateStage(t, source, "xid_copy", 1)
	sourceCopy.Columns[0].Usage = sourceType.Usage()
	if err := source.StageQueryTableRows(context.Background(), sourceStage, sourceCopy, rows, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, source, sourceStage)
	stage, d := queryCreateStage(t, target, "xid_copy", 1)
	d.Columns[0].Usage = targetType.Usage()
	queryCreatePublish(t, target, stage)
	candidate := catalog.CloneState(target.CatalogRead().State())
	imported := *d
	imported.Columns = append([]catalog.Column(nil), d.Columns...)
	imported.Columns[0].Usage = sourceType.Usage()
	candidate.SetDescriptor(d.Address(), &imported)
	stores := map[ids.ObjectAddress]*storage.Relation{d.Address(): source.Storage.Relations[sourceCopy.Address()]}
	if err := stored.ReownScalarTypes(candidate, stores, nil); err != nil {
		t.Fatal(err)
	}
	if candidate.MustRelation(d.Address()).Columns[0].Usage.Type != targetType {
		t.Fatal("XID column Usage retained foreign generation")
	}
	for i, row := range stores[d.Address()].Versions {
		value := row.Values[0]
		if value.Type() != targetType || value.IsNull() != (i == 2) {
			t.Fatal("XID value lost canonical generation/null", i, value)
		}
		if i < 2 {
			text, err := targetType.Output(context.Background(), value)
			literal, literalErr := targetType.SQLLiteral(context.Background(), value)
			want := []string{"0", max}[i]
			if err != nil || literalErr != nil || text != want || literal != "'"+want+"'" {
				t.Fatal("XID codec changed unsigned payload", text, literal, err, literalErr)
			}
		}
		if rows[i][0].Type() != sourceType {
			t.Fatal("retained source XID generation mutated")
		}
	}
}
