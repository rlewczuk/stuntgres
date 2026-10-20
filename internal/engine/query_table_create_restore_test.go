package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryTableCreateRestoreColumnAuthority(t *testing.T) {
	for _, mode := range []string{"raw", "forged", "isolated"} {
		e := New()
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(done)
		if mode != "raw" {
			operationReplay(op).tableDestination = true
		}
		if mode == "isolated" {
			e.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
		}
		for _, raw := range []parser.TypeName{{Name: "char", Quoted: true}, {Name: "regclass"}, {Name: "regclass", ArrayBounds: []int32{0}}, {Name: "xid"}, {Name: "xid", Schema: &parser.Identifier{Text: "pg_catalog"}}, {Name: "cid"}, {Name: "xid8"}} {
			e.mu.Lock()
			usage, err := e.resolveTableColumnType(raw, op)
			e.mu.Unlock()
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
				if len(raw.ArrayBounds) == 0 && usage.Type != testUsage(e, id).Type || len(raw.ArrayBounds) != 0 && (!usage.Type.IsArray() || usage.Type.Element() != testUsage(e, id).Type) {
					t.Fatal(mode, raw, usage)
				}
			} else if mode == "forged" {
				if err == nil || err.Error() != "stuntgres: invalid trusted restore column authority" {
					t.Fatal(mode, raw, err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
					t.Fatal(mode, raw, err)
				}
			}
		}
		// The private caller never changes ordinary cast or domain-base admission.
		e.mu.Lock()
		_, castErr := e.resolveAnnotationType(parser.TypeName{Name: "char", Quoted: true}, op, false)
		_, domainErr := e.resolveAnnotationTypeMode(parser.TypeName{Name: "char", Quoted: true}, op, false, true)
		_, arrayErr := e.resolveTableColumnType(parser.TypeName{Name: "char", Quoted: true, ArrayBounds: []int32{0}}, op)
		_, xidCastErr := e.resolveAnnotationType(parser.TypeName{Name: "xid"}, op, false)
		_, xidDomainErr := e.resolveAnnotationTypeMode(parser.TypeName{Name: "xid"}, op, false, true)
		_, xidArrayErr := e.resolveTableColumnType(parser.TypeName{Name: "xid", ArrayBounds: []int32{0}}, op)
		_, xidModifierErr := e.resolveTableColumnType(parser.TypeName{Name: "xid", Modifiers: []parser.TypeModifier{{Integer: new(int32)}}}, op)
		e.mu.Unlock()
		if xidCastErr != nil || xidDomainErr != nil {
			t.Fatal(mode, xidCastErr, xidDomainErr)
		}
		var modifier *sqlstate.Error
		if !errors.As(xidModifierErr, &modifier) || modifier.Code != "42601" || modifier.Message != `type modifier is not allowed for type "xid"` {
			t.Fatal(mode, xidModifierErr)
		}
		for _, err := range []error{castErr, domainErr, arrayErr, xidArrayErr} {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
				t.Fatal(mode, err)
			}
		}
	}
}

func TestQueryTableCreateRegclassArrayReownership(t *testing.T) {
	source := New()
	anchorStage, anchor := queryCreateStage(t, source, "anchor", 0)
	queryCreatePublish(t, source, anchorStage)
	regclass := testUsage(source, sqltype.Regclass).Type
	array, ok := source.CatalogRead().LookupArray(regclass)
	if !ok {
		t.Fatal("existing REGCLASS array descriptor is missing")
	}
	before := strconv.FormatUint(uint64(anchor.Address().ObjectID), 10)
	value, err := array.Input(context.Background(), "[0:1]={"+before+",NULL}", -1)
	if err != nil {
		t.Fatal(err)
	}
	target := New()
	target.catalog.nextOID = anchor.Address().ObjectID + 100
	targetAnchorStage, targetAnchor := queryCreateStage(t, target, "anchor", 0)
	queryCreatePublish(t, target, targetAnchorStage)
	targetArray, ok := target.CatalogRead().LookupArray(testUsage(target, sqltype.Regclass).Type)
	if !ok {
		t.Fatal("replacement REGCLASS array descriptor is missing")
	}
	sourceStage, sourceCopy := queryCreateStage(t, source, "copy", 1)
	sourceCopy.Columns[0].Usage = array.Usage()
	if err = source.stageQueryTableRows(context.Background(), sourceStage, sourceCopy, [][]sqltype.Value{{value}}, ids.TxRef{}); err != nil {
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
	stores := map[ids.ObjectAddress]*storage.Relation{d.Address(): source.storage.Relations[sourceCopy.Address()]}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{anchor.Address(): targetAnchor.Address()}
	if err = reownCatalogScalarTypes(candidate, stores, mapping); err != nil {
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

func TestQueryTableCreateRestoreReferenceLiteralBoundary(t *testing.T) {
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `"public"."anchor"`}}
	if err := restoreScalar(parser.RegclassExpr{Child: literal}, false); err != nil {
		t.Fatal("canonical stored reference literal rejected", err)
	}
	if err := restoreScalar(parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, false); err != nil {
		t.Fatal("ordinary stored NULL rejected", err)
	}
	for _, tc := range []struct {
		name     string
		child    parser.Expr
		accepted bool
	}{
		{"absent", nil, false},
		{"null_cast", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, true},
		{"numeric", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}, true},
		{"parameter", parser.ParameterExpr{Number: 1}, false},
		{"column", parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "n"}}}}, false},
		{"otherwise_restorable_call", parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "upper"}}}, Args: []parser.Expr{literal}}, true},
		{"effect", parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "nextval"}}}, Args: []parser.Expr{literal}}, false},
		{"query", parser.SubqueryExpr{Query: &parser.Select{Targets: []parser.Expr{literal}}}, false},
		{"nested_cast", parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "text"}}, true},
		{"nested_reference", parser.RegclassExpr{Child: literal}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := restoreScalar(parser.RegclassExpr{Child: tc.child}, false)
			if tc.name == "parameter" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "parameters are not allowed in restore" {
					t.Fatal("existing parameter rejection lost", err)
				}
				return
			}
			if tc.accepted {
				if err != nil {
					t.Fatal("selected restorable scalar child rejected", err)
				}
			} else if err == nil || err.Error() != "stuntgres: invalid restore scalar" {
				t.Fatal("unsupported scalar child admitted", err)
			}
		})
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
	source, target := New(), New()
	sourceType := testUsage(source, id).Type
	targetType := testUsage(target, id).Type
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
	if err := source.stageQueryTableRows(context.Background(), sourceStage, sourceCopy, rows, ids.TxRef{}); err != nil {
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
	stores := map[ids.ObjectAddress]*storage.Relation{d.Address(): source.storage.Relations[sourceCopy.Address()]}
	if err := reownCatalogScalarTypes(candidate, stores, nil); err != nil {
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
