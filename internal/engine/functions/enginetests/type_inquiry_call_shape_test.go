package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeCallShape(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	usage := func(id sqltype.ID) sqltype.Usage {
		t.Helper()
		d, ok := e.CatalogRead().LookupType(id)
		if !ok { t.Fatal("missing real declaration type", id) }
		return d.Usage()
	}
	for _, tc := range []struct {
		name string
		formals []sqltype.ID
		inputs []sqltype.Usage
		want bool
	}{
		{"pg_typeof", []sqltype.ID{2276}, []sqltype.Usage{{}}, true},
		{"pg_typeof", []sqltype.ID{2276}, []sqltype.Usage{usage(sqltype.Unknown)}, true},
		{"pg_typeof", []sqltype.ID{2276}, []sqltype.Usage{usage(sqltype.Int4)}, true},
		{"pg_typeof", []sqltype.ID{2276}, []sqltype.Usage{usage(sqltype.Text)}, true},
		{"pg_typeof", []sqltype.ID{sqltype.Text}, []sqltype.Usage{usage(sqltype.Text)}, false},
		{"pg_typeof", []sqltype.ID{2276}, nil, false},
		{"format_type", []sqltype.ID{sqltype.Oid, sqltype.Int4}, []sqltype.Usage{usage(sqltype.Int4), {}}, true},
		{"format_type", []sqltype.ID{sqltype.Oid, sqltype.Int4}, []sqltype.Usage{usage(sqltype.Bool), {}}, false},
		{"format_type", []sqltype.ID{sqltype.Oid, sqltype.Text}, []sqltype.Usage{{}, {}}, false},
		{"pg_basetype", []sqltype.ID{sqltype.Regtype}, []sqltype.Usage{{}}, true},
		{"pg_basetype", []sqltype.ID{sqltype.Regtype}, []sqltype.Usage{usage(sqltype.Regtype)}, true},
		{"pg_basetype", []sqltype.ID{sqltype.Regtype}, []sqltype.Usage{usage(sqltype.Bool)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition, ok := functions.Lookup(tc.name)
			if !ok || definition.ProbeCall == nil { t.Fatal("missing family probe",tc.name) }
			declared := make([]sqltype.Usage,len(tc.formals))
			for i,id := range tc.formals { declared[i] = usage(id) }
			targets := append([]sqltype.Usage(nil),declared...)
			operands := make([]functions.Argument,len(tc.inputs))
			for i,u := range tc.inputs { operands[i].Usage = u }
			original := append([]functions.Argument(nil),operands...)
			dependencies := len(b.Dependencies)
			parameters := len(b.Params.Parameters)
			raw := parser.FunctionExpr{Name:parser.ObjectName{Parts:[]parser.Identifier{{Text:tc.name}}}}
			got := definition.ProbeCall(b,raw,operands,declared,targets)
			if got != tc.want { t.Fatal("applicability",got,tc.want) }
			if !reflect.DeepEqual(operands,original) && len(operands) != 0 || len(b.Dependencies) != dependencies || len(b.Params.Parameters) != parameters { t.Fatal("probe changed analysis state") }
			if got {
				want := declared
				if tc.name == "pg_typeof" { want = tc.inputs }
				if !reflect.DeepEqual(targets,want) { t.Fatal("logical target usage changed",targets,want) }
				// Fixed VARIADIC decoration does not change a matching family
				// signature; ordinary binding owns its diagnostics and transport.
				raw.Variadic = true
				if !definition.ProbeCall(b,raw,operands,declared,targets) { t.Fatal("fixed VARIADIC applicability lost") }
			}
		})
	}
}
