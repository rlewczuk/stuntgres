package core_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Install immutable declaration/default owners directly. The witnesses below
// inspect the real common binder's selected identity, assembly and provenance;
// they are not native-only public SQL outcome scenarios.
func sqlExecutionUnitProcedure(t *testing.T, s *core.StatementScope, name string, oid ids.OID, formals []proc.Formal) *catalog.Procedure {
	t.Helper()
	e := s.Engine()
	read := e.CatalogRead()
	d := &proc.Definition{Name: name, Procedure: true, Source: "SELECT 1"}
	var err error
	d.Statements, err = parser.Parse(d.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFormals(formals); err != nil {
		t.Fatal(err)
	}
	if len(d.OutputFormals) == 0 {
		v, _ := read.LookupType(sqltype.Void)
		d.Result = v.Usage()
	} else {
		v, err := d.OutputTuple()
		if err != nil {
			t.Fatal(err)
		}
		d.Result = v.Usage()
	}
	signature := make([]string, len(d.Inputs))
	for i, u := range d.Inputs {
		signature[i] = strconv.FormatUint(uint64(u.Type.ID()), 10)
	}
	language, err := catalog.LookupLanguage(read, "sql")
	if err != nil {
		t.Fatal(err)
	}
	p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: name, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.ProcedureRoutine, SQL: d, Result: ids.OID(d.Result.Type.ID()), Language: language.Address(), Volatility: expr.Volatile, Parallel: 'u', Cost: 100}
	entries := make([]expr.Expr, len(d.Inputs))
	for i, formal := range d.InputFormals {
		if d.Formals[formal].HasDefault {
			var v sqltype.Value
			if d.Inputs[i].Type.IsArray() {
				v, err = sqltype.ArrayValue(d.Inputs[i].Type, nil, nil)
			} else {
				v, err = sqltype.Integer(d.Inputs[i].Type, 4)
			}
			if err != nil {
				t.Fatal(err)
			}
			entries[i] = plan.LiteralExpr{Info: expr.Info{Usage: d.Inputs[i]}, Value: v}
		}
	}
	p.SQLDefaults, err = stored.NewRoutineDefaults(context.Background(), read, nil, entries)
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(p.Address(), p)
	state.BindName(catalog.DescriptorNameKey(p), p.Address())
	index := catalog.ProcedureIndexEdit{Index: state.ProcedureNames()}
	index.Replace(nil, p)
	state.SetProcedureNames(index.Index)
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	return p
}

func TestSQLCallableProcedureOutputAssociation(t *testing.T) {
	s, owner, f, read := loopUnitFrame(t, "DECLARE a integer; b integer; narrow smallint; BEGIN RETURN 7; END")
	integer, _ := read.LookupType(sqltype.Int4)
	formals := []proc.Formal{{Name: "first", Mode: "o", Usage: integer.Usage()}, {Name: "input", Mode: "i", Usage: integer.Usage(), HasDefault: true}, {Name: "last", Mode: "b", Usage: integer.Usage(), HasDefault: true}}
	selected := sqlExecutionUnitProcedure(t, s, "sql_execution_association", 91001, formals)
	before := append([]proc.Formal(nil), selected.SQL.Formals...)
	body := owner.SQL.PL.Root.Statements[0].Block
	f.Enter(owner.SQL.PL.Root)
	defer f.Leave(owner.SQL.PL.Root)
	f.Enter(body)
	defer f.Leave(body)
	four, _ := sqltype.Integer(integer, 4)
	f.Values[body.Bindings["a"]], f.Values[body.Bindings["b"]] = four, four
	narrow, _ := read.LookupType(sqltype.Int2)
	f.Values[body.Bindings["narrow"]] = sqltype.Null(narrow)
	e := s.Engine()
	for _, tc := range []struct {
		query             string
		slots             []int
		written, children int
	}{
		{"CALL pg_catalog.sql_execution_association(last=>b,first=>a,input=>9)", []int{body.Bindings["a"], body.Bindings["b"]}, 2, 2},
		{"CALL pg_catalog.sql_execution_association(a,last=>b)", []int{body.Bindings["a"], body.Bindings["b"]}, 1, 2},
		{"CALL pg_catalog.sql_execution_association(a)", []int{body.Bindings["a"], -1}, 0, 2},
		{"CALL pg_catalog.sql_execution_association(narrow,last=>b)", []int{-1, body.Bindings["b"]}, 1, 2},
	} {
		statements, err := parser.Parse(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		raw := statements[0].(*parser.Call).Function
		shape, ok := analyze.SQLCallableShape(e.CatalogRead(), nil, selected, raw)
		if !ok || len(shape.OutputMap) != 2 || len(shape.InputMap) != 2 {
			t.Fatal("common candidate lost output/full-input map")
		}
		auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
		e.Mu.Lock()
		binding, err := analyze.DescribePL(context.Background(), e.AnalysisHost(s.Op), statements[0], core.AuthorityOf(auth), ids.ObjectRef{}, s.Input, nil, f, nil)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(tc.query, err)
		}
		if len(binding.PLCallOutputs) != len(tc.slots) {
			t.Fatal("missing winning destination projection")
		}
		for i, slot := range tc.slots {
			out := binding.PLCallOutputs[i]
			if out.Target.Slot != slot || out.Formal != []int{1, 3}[i] || out.Name != []string{"first", "last"}[i] {
				t.Fatal("written order/coercion/default invented destination", out)
			}
		}
		node, ok := binding.Expressions[0][0].(plan.SQLRoutineExpr)
		if !ok || node.Procedure != selected || node.WrittenCount != tc.written || len(node.Children()) != tc.children || len(node.InputMap) != 2 {
			t.Fatal("output became executable demand or common winner changed", node)
		}
		if !reflect.DeepEqual(before, selected.SQL.Formals) || shape.OutputMap[0].Start < 0 {
			t.Fatal("binding mutated declaration or candidate output backing")
		}
		if len(binding.PLCallOutputs) > 0 {
			binding.PLCallOutputs[0].Target.Slot = 999
		}
		if f.Values[body.Bindings["a"]] != four || owner.SQL.PL.ValidateDefinition(owner.SQL) != nil {
			t.Fatal("projection retained authority into caller program")
		}
	}
	// Expanded and explicit variadic positions come from the same common source
	// projection; outputs never enter the input-only assembly.
	array, _ := read.LookupType(1007)
	variadic := sqlExecutionUnitProcedure(t, s, "sql_execution_variadic", 91002, []proc.Formal{{Name: "answer", Mode: "o", Usage: integer.Usage()}, {Name: "xs", Mode: "v", Usage: array.Usage(), HasDefault: true}})
	for _, q := range []string{"CALL pg_catalog.sql_execution_variadic(a,30,7)", "CALL pg_catalog.sql_execution_variadic(a,VARIADIC ARRAY[30,7])", "CALL pg_catalog.sql_execution_variadic(a)"} {
		parsed, err := parser.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		candidate, ok := analyze.SQLCallableShape(e.CatalogRead(), nil, variadic, parsed[0].(*parser.Call).Function)
		if !ok || len(candidate.OutputMap) != 1 || candidate.OutputMap[0].Start != 0 || len(candidate.InputMap) != 1 || candidate.InputMap[0].Start == 0 {
			t.Fatal("variadic projection displaced output", candidate)
		}
	}
}
