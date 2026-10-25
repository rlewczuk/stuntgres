package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func TestGeometryConversionMixedReplayProvenance(t *testing.T) {
	e := New().RestoreStage()
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
	if err := scope.Gate(source); err != nil {
		t.Fatal(err)
	}
	auth, err := core.OperationAuthorization(op)
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
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	u := relation.Columns[0].Usage
	value, provenance, err := typedef.RestoreDomainReplayNode(DomainReplayEnv(e, &c, op, auth), u, &mixed)
	if err != nil || provenance != typedef.DomainReplayDomainNull {
		t.Fatal("mixed reconstruction provenance", provenance, err)
	}
	capture, err := CaptureDomainReplay(e, scope, source, record, auth, relation)
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
	value = capture.Cells[0].Value
	check("admitted exact cell")
}

func TestGeometryConversionMixedReplayBoundedRow(t *testing.T) {
	e := New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.conversion_row_required AS integer NOT NULL"))
	domainUnitTable(t, e, op, "CREATE TABLE public.conversion_row_leaf(c circle)")
	relation := domainUnitTable(t, e, op, "CREATE TABLE public.conversion_row_destination(v public.conversion_row_required[],a public.conversion_row_leaf[])")
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	identity := []string{"public", "conversion_row_required"}
	elements := []typedef.DomainReplayNode{{Kind: "domain_null", Type: &identity}}
	dimensions := []typedef.DomainReplayDimension{{Lower: -4, Length: 1}}
	array := typedef.DomainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements}
	record := typedef.DomainReplayRecord{Schema: "public", Table: "conversion_row_destination", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &array}}}
	input, _ := op.Session.Settings.Presentation()
	input.ExactSnapshot = true
	values := ` VALUES(NULL,CAST(ARRAY[ROW(pg_catalog.circle('(1,2)'::pg_catalog.point,'-1'::pg_catalog.float8))::public.conversion_row_leaf,ROW(pg_catalog.circle('(3,4)'::pg_catalog.point,'-2'::pg_catalog.float8))::public.conversion_row_leaf] AS public.conversion_row_leaf[]))`
	source := statement(t, "INSERT INTO public.conversion_row_destination(v,a[-2:-1])"+values).(*parser.Insert)
	rows, err := importMaterializedSnapshotRow(e, op.Context(), op, auth, relation, source, input, &record)
	if err != nil {
		t.Fatal("unrelated constructor bounds rejected authentic replay", err)
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatal("mixed row shape", rows)
	}
	domainArray, circles := rows[0][0], rows[0][1]
	if domainArray.ArrayDimension(0).Lower != -4 || domainArray.ArrayLen() != 1 || !domainArray.ArrayElement(0).IsNull() || domainArray.ArrayElement(0).Type() != relation.Columns[0].Usage.Type.Element() {
		t.Fatal("captured domain NULL shape or identity lost")
	}
	if circles.ArrayRank() != 1 || circles.ArrayDimension(0).Lower != -2 || circles.ArrayLen() != 2 {
		t.Fatal("ordinary bounded constructor shape lost")
	}
	for i, want := range []string{"<(1,2),-1>", "<(3,4),-2>"} {
		circle := circles.ArrayElement(i).CompositeField(0)
		got, err := circle.Type().Output(op.Context(), circle)
		if err != nil || got != want {
			t.Fatal("ordinary constructor leaf", i, got, err)
		}
	}
	for _, targets := range []string{"v[-4:-4],a[-2:-1]", "v,a[-2:-2]", "v,a[-2:0]"} {
		source := statement(t, "INSERT INTO public.conversion_row_destination("+targets+")"+values).(*parser.Insert)
		if _, err := importMaterializedSnapshotRow(e, op.Context(), op, auth, relation, source, input, &record); err == nil {
			t.Fatal("partial captured target or changed constructor shape admitted", targets)
		}
	}
}
