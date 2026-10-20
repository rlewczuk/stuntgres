package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// This proof invokes the real binder and its real family priority. It does not
// maintain another resolver or use production admission facts to choose inputs.
// The 799-candidate universe and supported expectations are independent release
// evidence; concrete column occurrences prevent literal/NULL normalization from
// masquerading as an executable overload.
func TestPGOperatorProviderAgreement(t *testing.T) {
	e := New()
	run(t, e, "CREATE TYPE operator_provider_enum AS ENUM('a','b')")
	run(t, e, "CREATE TYPE operator_provider_record AS (n INT)")
	view := e.CatalogRead()
	userType := func(name string) *sqltype.Descriptor {
		for _, d := range view.Descriptors() {
			if typ, ok := d.(*catalog.Type); ok && typ.Name() == name {
				return typ.SQLType
			}
		}
		t.Fatal("missing witness type", name)
		return nil
	}
	enum, record := userType("operator_provider_enum"), userType("operator_provider_record")
	integer := func(s string) sqltype.ID {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		return sqltype.ID(n)
	}
	physical := func(id sqltype.ID, textArray bool) *sqltype.Descriptor {
		switch id {
		case 0:
			return nil
		case 2277, 5078:
			id = 1007
			if textArray {
				id = 1009
			}
		case 2283, 2776:
			id = sqltype.Int4
		case 5077, 5079:
			id = sqltype.Int4
			if textArray {
				id = sqltype.Text
			}
		case 3500:
			return enum
		case 3831, 5080:
			id = 3904
		case 4537, 4538:
			id = 4451
		case 2249:
			return record
		}
		d, _ := view.LookupType(id)
		return d
	}
	want := map[ids.OID][]string{}
	for _, row := range pgOperatorReleaseRows(t, "pg-operator-pg18-supported.tsv", 15) {
		id := ids.OID(integer(row[0]))
		if want[id] != nil {
			t.Fatal("duplicate supported expected OID", id)
		}
		want[id] = row
	}
	production := map[ids.OID]routine.OperatorMetadata{}
	for _, fact := range routine.SupportedOperatorMetadata {
		if _, exists := production[fact.OID]; exists {
			t.Fatal("duplicate production OID", fact.OID)
		}
		production[fact.OID] = fact
	}
	universe := pgOperatorReleaseRows(t, "regoperator-pg18-inventory.tsv", 8)
	seen, admitted := map[ids.OID]bool{}, map[ids.OID]bool{}
	for _, candidate := range universe {
		oid := ids.OID(integer(candidate[0]))
		if seen[oid] {
			t.Fatal("duplicate candidate", oid)
		}
		seen[oid] = true
		leftID, rightID := integer(candidate[3]), integer(candidate[4])
		t.Run(fmt.Sprint(oid), func(t *testing.T) {
			if oid == 3552 {
				// This released predicate is executable only through the bounded
				// exclusion owner; advertising it does not enable scalar SQL.
				inet := physical(sqltype.Inet, false)
				class, ok := sqltype.LookupGiSTIndexClass("inet_ops")
				operator, err := sqltype.BindExclusionOperator(inet.Usage(), class, "&&")
				inputs, signatureErr := operator.OperatorInputs()
				if !ok || err != nil || signatureErr != nil || inputs != ([2]sqltype.ID{sqltype.Inet, sqltype.Inet}) || operator.IsEquality() {
					t.Fatal("exclusion provider signature", operator, err, signatureErr)
				}
				left, err := inet.Input(context.Background(), "10.0.0.1/24", -1)
				if err != nil {
					t.Fatal(err)
				}
				right, err := inet.Input(context.Background(), "10.0.0.2", -1)
				if err != nil {
					t.Fatal(err)
				}
				match, err := operator.Matches(context.Background(), left, right)
				if err != nil || !match {
					t.Fatal("exclusion provider evaluation", match, err)
				}
				if _, ok := want[oid]; !ok || production[oid].Result != 16 {
					t.Fatal("missing released metadata", oid)
				}
				admitted[oid] = true
				return
			}
			// Repeat generic array declarations with two genuine element types.
			witnesses := []bool{false}
			if leftID == 2277 || leftID == 5078 || rightID == 2277 || rightID == 5078 {
				witnesses = append(witnesses, true)
			}
			for _, textArray := range witnesses {
				left, right := physical(leftID, textArray), physical(rightID, textArray)
				executable := (leftID == 0 || left != nil && left.Concrete()) && right != nil && right.Concrete()
				var bound expr.Expr
				var err error
				if executable {
					b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
					b.Target.Relation = &catalog.Relation{Columns: []catalog.Column{{Name: "l"}, {Name: "r", Usage: right.Usage()}}, Attributes: []int32{1, 2}, Index: map[string]int{"l": 0, "r": 1}}
					if left != nil {
						b.Target.Relation.Columns[0].Usage = left.Usage()
					}
					column := func(name string) parser.Expr {
						return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
					}
					args := []parser.Expr{column("r")}
					if leftID != 0 {
						args = []parser.Expr{column("l"), column("r")}
					}
					raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: candidate[2]}}}, Args: args}
					bound, err = b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
					executable = err == nil
				}
				if executable {
					var operands []expr.Expr
					plan.ExpressionChildren(bound, func(child expr.Expr) {
						if child != nil {
							operands = append(operands, child)
						}
					})
					if leftID == 0 {
						executable = len(operands) == 1 && operands[0].ResultUsage().Type == right
					} else {
						executable = len(operands) == 2 && operands[0].ResultUsage().Type == left && operands[1].ResultUsage().Type == right
					}
				}
				expected, included := want[oid]
				if executable != included {
					t.Errorf("provider admission=%t expected=%t selected=%T error=%v, declared operands %d/%d", executable, included, bound, err, leftID, rightID)
					continue
				}
				fact, installed := production[oid]
				if installed != included {
					t.Errorf("production admission=%t expected=%t", installed, included)
				}
				if executable {
					result := physical(integer(expected[9]), textArray)
					if bound.ResultUsage().Type != result || fact.Result != uint32(integer(expected[9])) {
						t.Errorf("result=%v declared=%s physical=%v production=%d", bound.ResultUsage(), expected[9], result, fact.Result)
					}
					admitted[oid] = true
				}
			}
		})
	}
	if len(seen) != len(universe) || len(admitted) != len(want) || len(production) != len(want) {
		t.Errorf("partition visited=%d admitted=%d production=%d expected=%d", len(seen), len(admitted), len(production), len(want))
	}
	for oid := range production {
		if !seen[oid] {
			t.Error("invented production identity", oid)
		}
	}
	t.Run("polymorphic-relationships", pgOperatorPolymorphicRelationships)
}
