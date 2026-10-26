package proc

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pseudoUsages() map[sqltype.ID]sqltype.Usage {
	types := make(map[sqltype.ID]sqltype.Usage)
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ.Usage() }
	return types
}

func TestPseudoSignatureValidation(t *testing.T) {
	u := pseudoUsages()
	for _, tc := range []struct{inputs []sqltype.ID; result sqltype.ID; language,code,message string}{
		{nil,2283,"sql","42P13","cannot determine result data type"},
		{[]sqltype.ID{2283},3831,"sql","42P13","cannot determine result data type"},
		{nil,2281,"plpgsql","42P13",`unsafe use of pseudo-type "internal"`},
		{[]sqltype.ID{2281},2281,"sql","42P13","SQL functions cannot return type internal"},
		{[]sqltype.ID{sqltype.Void},sqltype.Int4,"plpgsql","0A000","PL/pgSQL functions cannot accept type void"},
		{[]sqltype.ID{2276},sqltype.Int4,"sql","42P13",`SQL functions cannot have arguments of type "any"`},
		{[]sqltype.ID{2276},sqltype.Int4,"plpgsql","0A000",`PL/pgSQL functions cannot accept type "any"`},
		{nil,2276,"sql","42P13",`SQL functions cannot return type "any"`},
		{nil,2276,"plpgsql","0A000",`PL/pgSQL functions cannot return type "any"`},
		{[]sqltype.ID{2283},2283,"sql","",""},
		{[]sqltype.ID{4537},3831,"sql","",""},
		{[]sqltype.ID{4538},5080,"sql","",""},
	} {
		inputs := make([]sqltype.Usage,len(tc.inputs)); for i,id := range tc.inputs { inputs[i] = u[id] }
		formals := make([]Formal,len(inputs)); for i,input := range inputs { formals[i] = Formal{Mode:"i",Usage:input} }
		err := ValidatePseudoSignature(inputs,[]sqltype.Usage{u[tc.result]},formals,tc.language,false)
		if tc.code == "" { if err != nil { t.Fatal(err) }; continue }
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != "" { t.Fatalf("%+v: %v",tc,err) }
	}
	definition := &Definition{Result:u[2283]}
	if err := definition.SetFormals([]Formal{{Mode:"v",Name:"xs",Usage:u[2277]}}); err != nil { t.Fatal(err) }
	if definition.Inputs[0] != u[2277] || u[2277].Type.Concrete() { t.Fatal("declaration admission granted datums") }
}

func TestPseudoInvocationSignature(t *testing.T) {
	testPseudoPLProjection(t)
	u := pseudoUsages()
	d := &Definition{Inputs:[]sqltype.Usage{u[2283],u[5077],u[5077]},Result:u[2283]}
	s := InvocationSignature{Inputs:[]sqltype.Usage{u[sqltype.Int4],u[sqltype.Numeric],u[sqltype.Numeric]},Result:u[sqltype.Int4]}
	if !s.ValidFor(d) || !s.Equal(s) { t.Fatal("lawful independent families rejected") }
	bad := InvocationSignature{Inputs:s.Inputs,Result:u[sqltype.Text]}
	if bad.ValidFor(d) || bad.Equal(s) { t.Fatal("forged result accepted") }
	if d.Inputs[0] != u[2283] || d.Result != u[2283] { t.Fatal("published declaration mutated") }
	array := &Definition{Inputs:[]sqltype.Usage{u[2277],u[2283]},Result:u[2277]}
	if !(InvocationSignature{Inputs:[]sqltype.Usage{u[1007],u[sqltype.Int4]},Result:u[1007]}).ValidFor(array) { t.Fatal("array-element relation rejected") }
	if (InvocationSignature{Inputs:[]sqltype.Usage{u[1007],u[sqltype.Text]},Result:u[1007]}).ValidFor(array) { t.Fatal("array-element mismatch accepted") }
	for _, tc := range []struct{kind InvocationKind; result sqltype.ID}{
		{DMLTriggerInvocation,2279},{EventTriggerInvocation,3838},
	} {
		handler := &Definition{Invocation:tc.kind,Result:u[tc.result]}
		call := InvocationSignature{Result:u[tc.result]}
		if !call.ValidFor(handler) { t.Fatal("handler call must reach the trigger-only runtime diagnostic") }
		call.Result = u[2281]
		if call.ValidFor(handler) { t.Fatal("handler declaration granted unrelated pseudo invocation authority") }
	}
}
