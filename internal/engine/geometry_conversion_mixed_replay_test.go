package engine

import (
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func TestGeometryConversionMixedReplayProvenance(t *testing.T) {
	e := New().restoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.conversion_mixed_required AS integer NOT NULL"))
	domainUnitTable(t, e, op, "CREATE TABLE public.conversion_mixed_leaf(c circle)")
	domainUnitTable(t, e, op, "CREATE TABLE public.conversion_mixed_carrier(n public.conversion_mixed_required,a public.conversion_mixed_leaf[])")
	relation := domainUnitTable(t, e, op, "CREATE TABLE public.conversion_mixed_destination(v public.conversion_mixed_carrier)")
	source := statement(t, "INSERT INTO public.conversion_mixed_destination VALUES(NULL)").(*parser.Insert)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}()
	if err := scope.gate(source); err != nil {
		t.Fatal(err)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	identity := []string{"public", "conversion_mixed_required"}
	domainNull := typedef.DomainReplayNode{Kind: "domain_null", Type: &identity}
	leaf := func(text string) typedef.DomainReplayNode {
		scalar := typedef.DomainReplayNode{Kind: "scalar", Scalar: &text}
		members := []typedef.DomainReplayMember{{Name: "c", Value: &scalar}}
		return typedef.DomainReplayNode{Kind: "composite", Members: &members}
	}
	elements := []typedef.DomainReplayNode{
		leaf("pg_catalog.circle('(1,2)'::pg_catalog.point,'-1'::pg_catalog.float8)"),
		leaf("pg_catalog.circle('(3,4)'::pg_catalog.point,'-Infinity'::pg_catalog.float8)"),
	}
	dimensions := []typedef.DomainReplayDimension{{Lower: -2, Length: 2}}
	array := typedef.DomainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements}
	members := []typedef.DomainReplayMember{{Name: "n", Value: &domainNull}, {Name: "a", Value: &array}}
	mixed := typedef.DomainReplayNode{Kind: "composite", Members: &members}
	record := typedef.DomainReplayRecord{Schema: "public", Table: "conversion_mixed_destination", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &mixed}}}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	u := relation.Columns[0].Usage
	value, provenance, err := restoreDomainReplayNode(&c, e, op, auth, u, &mixed)
	if err != nil || provenance != domainReplayDomainNull|domainReplayNegativeCircle {
		t.Fatal("mixed reconstruction provenance", provenance, err)
	}
	capture, err := e.captureDomainReplay(scope, source, record, auth, relation)
	if err != nil {
		t.Fatal("mixed cell admission", err)
	}
	check := func(label string) {
		t.Helper()
		null := value.CompositeField(0)
		if !null.IsNull() || null.Type() != u.Type.CompositeMember(0).Usage.Type || !null.Type().IsDomain() {
			t.Fatal(label, "lost authentic domain NULL identity")
		}
		a := value.CompositeField(1)
		if a.ArrayRank() != 1 || a.ArrayLen() != 2 || a.ArrayDimension(0).Lower != -2 || a.ArrayDimension(0).Length != 2 {
			t.Fatal(label, "array shape lost")
		}
		for i, want := range []string{"<(1,2),-1>", "<(3,4),-Infinity>"} {
			circle := a.ArrayElement(i).CompositeField(0)
			got, err := circle.Type().Output(op.Context(), circle)
			if err != nil || got != want || !circle.RequiresCircleConstructorReplay() {
				t.Fatal(label, i, got, err)
			}
		}
	}
	check("reconstruction")
	value = capture.cells[0].Value
	check("admitted exact cell")
}
