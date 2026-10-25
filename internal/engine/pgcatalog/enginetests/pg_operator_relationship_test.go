package enginetests_test

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgOperatorRelationshipBind(t *testing.T, e *core.Engine, name string, left, right *sqltype.Descriptor) (expr.Expr, error) {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Target.Relation = &catalog.Relation{Columns: []catalog.Column{{Name: "l", Usage: left.Usage()}, {Name: "r", Usage: right.Usage()}}, Attributes: []int32{1, 2}, Index: map[string]int{"l": 0, "r": 1}}
	bindTestTargetNamespace(b)
	column := func(name string) parser.Expr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	return b.Bind(parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{column("l"), column("r")}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
}

func TestPGOperatorPolymorphicRelationships(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TYPE operator_left_enum AS ENUM('a','b')")
	enginetest.Run(t, e, "CREATE TYPE operator_right_enum AS ENUM('a','b')")
	view := e.CatalogRead()
	typ := func(id sqltype.ID) *sqltype.Descriptor {
		d, ok := view.LookupType(id)
		if !ok {
			t.Fatal("missing physical witness", id)
		}
		return d
	}
	released := map[ids.OID][]string{}
	for _, row := range pgOperatorReleaseRows(t, "pg-operator-pg18-supported.tsv", 15) {
		for _, oid := range []ids.OID{349, 374, 375, 2779, 2780} {
			if row[0] == strconv.FormatUint(uint64(oid), 10) {
				released[oid] = row
			}
		}
	}
	for _, tc := range []struct {
		name                                                       string
		oid                                                        ids.OID
		inputLeft, inputRight, selectedLeft, selectedRight, result sqltype.ID
		declaredLeft, declaredRight, declaredResult                string
		array                                                      bool
	}{
		{"concat-promotes-left", 375, 1005, 1007, 1007, 1007, 1007, "5078", "5078", "5078", true},
		{"concat-promotes-right", 375, 1007, 1005, 1007, 1007, 1007, "5078", "5078", "5078", true},
		{"append-promotes-array", 349, 1005, 23, 1007, 23, 1007, "5078", "5077", "5078", true},
		{"append-promotes-element", 349, 1007, 21, 1007, 23, 1007, "5078", "5077", "5078", true},
		{"prepend-promotes-array", 374, 23, 1005, 23, 1007, 1007, "5077", "5078", "5078", true},
		{"prepend-promotes-element", 374, 21, 1007, 23, 1007, 1007, "5077", "5078", "5078", true},
		{"text-nonarray", 2779, 25, 23, 25, 23, 25, "25", "2776", "25", false},
		{"nonarray-text", 2780, 23, 25, 23, 25, 25, "2776", "25", "25", false},
		{"text-array-is-prepend", 374, 25, 1009, 25, 1009, 1009, "5077", "5078", "5078", true},
		{"array-text-is-append", 349, 1009, 25, 1009, 25, 1009, "5078", "5077", "5078", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := released[tc.oid]
			if len(row) != 15 || row[7] != tc.declaredLeft || row[8] != tc.declaredRight || row[9] != tc.declaredResult {
				t.Fatal("released generic identity", tc, row)
			}
			identity := view.MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: tc.oid})
			if strconv.FormatUint(uint64(identity.Left), 10) != tc.declaredLeft || strconv.FormatUint(uint64(identity.Right), 10) != tc.declaredRight {
				t.Fatal("identity became a concrete overload", identity)
			}
			bound, err := pgOperatorRelationshipBind(t, e, "||", typ(tc.inputLeft), typ(tc.inputRight))
			if err != nil {
				t.Fatal(err)
			}
			_, array := bound.(functions.ArrayTransform)
			_, text := bound.(functions.String)
			if array != tc.array || text == tc.array {
				t.Fatalf("wrong actual dispatch %T, want array=%t", bound, tc.array)
			}
			var operands []expr.Expr
			plan.ExpressionChildren(bound, func(child expr.Expr) {
				if child != nil {
					operands = append(operands, child)
				}
			})
			if len(operands) != 2 {
				t.Fatal("selected arity", len(operands))
			}
			if operands[0].ResultUsage().Type != typ(tc.selectedLeft) || operands[1].ResultUsage().Type != typ(tc.selectedRight) || bound.ResultUsage().Type != typ(tc.result) {
				t.Fatal("selected/coerced operands or result differ", operands[0].ResultUsage(), operands[1].ResultUsage(), bound.ResultUsage(), tc)
			}
		})
	}
	var leftEnum, rightEnum *sqltype.Descriptor
	for _, d := range view.Descriptors() {
		if d, ok := d.(*catalog.Type); ok {
			switch d.Name() {
			case "operator_left_enum":
				leftEnum = d.SQLType
			case "operator_right_enum":
				rightEnum = d.SQLType
			}
		}
	}
	if leftEnum == nil || rightEnum == nil || leftEnum == rightEnum {
		t.Fatal("distinct genuine enum witnesses missing")
	}
	for _, tc := range []struct {
		name, operator string
		left, right    *sqltype.Descriptor
	}{
		{"simple-array-incompatible-elements", "@>", typ(1007), typ(1009)},
		{"distinct-enum-authorities", "=", leftEnum, rightEnum},
		{"range-unrelated-element", "@>", typ(sqltype.Int4Range), typ(sqltype.Bool)},
		{"range-unrelated-family", "&&", typ(sqltype.Int4Range), typ(sqltype.DateRange)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bound, err := pgOperatorRelationshipBind(t, e, tc.operator, tc.left, tc.right)
			var diagnostic *sqlstate.Error
			if bound != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
				t.Fatal("invalid polymorphic relationship admitted", bound, err)
			}
		})
	}
}

// Scalar unit fixtures explicitly admit their input row, just like production owners.
func bindTestTargetNamespace(b *analyze.Binder) {
	relation := b.Target.Relation
	columns := make([]analyze.QueryColumn, len(relation.Columns))
	for i, column := range relation.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Physical: i, Leaf: analyze.QueryTargetLeaf}
	}
	qualifier := analyze.QueryQualifier{Name: relation.Name(), Original: relation.Name(), Ref: b.Target.Ref, Columns: columns}
	if relation.Namespace() != (ids.ObjectAddress{}) {
		qualifier.Schema = b.BindingCatalog().Get(relation.Namespace()).Name()
	}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: relation.Columns}
}

func pgOperatorReleaseRows(t *testing.T, name string, width int) [][]string {
	t.Helper()
	f, err := os.Open("../../../../docs/design/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = '\t', width, true
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows[1:]
}
