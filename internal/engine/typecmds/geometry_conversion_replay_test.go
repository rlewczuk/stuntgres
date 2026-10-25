package typecmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestGeometryConversionReplayShapes(t *testing.T) {
	e := engine.New().RestoreStage()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitTable(t, e, op, "CREATE TABLE public.conversion_replay_member(c circle)")
	relation := domainUnitTable(t, e, op, "CREATE TABLE public.conversion_replay_array(a public.conversion_replay_member[],c circle)")
	source := enginetest.Statement(t, "INSERT INTO public.conversion_replay_array VALUES(NULL,NULL)").(*parser.Insert)
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
	scalar := func(text string) typedef.DomainReplayNode {
		return typedef.DomainReplayNode{Kind: "scalar", Scalar: &text}
	}
	negative := scalar("pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)")
	positive := scalar("CAST('<(0,0),1>' AS circle)")
	member := func(n typedef.DomainReplayNode) typedef.DomainReplayNode {
		members := []typedef.DomainReplayMember{{Name: "c", Value: &n}}
		return typedef.DomainReplayNode{Kind: "composite", Members: &members}
	}
	dimensions := []typedef.DomainReplayDimension{{Lower: -2, Length: 2}, {Lower: 3, Length: 2}}
	elements := []typedef.DomainReplayNode{member(negative), member(positive), {Kind: "null"}, member(typedef.DomainReplayNode{Kind: "null"})}
	array := typedef.DomainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements}
	record := func(column string, node *typedef.DomainReplayNode) typedef.DomainReplayRecord {
		return typedef.DomainReplayRecord{Schema: "public", Table: "conversion_replay_array", Cells: []typedef.DomainReplayCell{{Column: column, Value: node}}}
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	usage := relation.Columns[0].Usage
	value, provenance, err := typedef.RestoreDomainReplayNode(typecmds.DomainReplayEnv(e, &c, op, auth), usage, &array)
	if err != nil || provenance != 0 {
		t.Fatal("constructor reconstruction", provenance, err)
	}
	if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("a", &array), auth, relation); err == nil {
		t.Fatal("constructor-only array acquired private domain NULL provenance")
	}
	if bounds := regobj.ConstructorArrayTargetBounds(value); bounds != "[-2:-1][3:4]" {
		t.Fatal("constructor target bounds", bounds)
	}
	if value.ArrayRank() != 2 || value.ArrayDimension(0).Lower != -2 || value.ArrayDimension(1).Lower != 3 || value.ArrayLen() != 4 {
		t.Fatal("shape lost", value)
	}
	if value.ArrayElement(0).CompositeField(0).RequiresCircleConstructorReplay() != true || value.ArrayElement(1).CompositeField(0).RequiresCircleConstructorReplay() || !value.ArrayElement(2).IsNull() || value.ArrayElement(3).IsNull() || !value.ArrayElement(3).CompositeField(0).IsNull() {
		t.Fatal("leaf or NULL identity lost")
	}
	if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("c", &negative), auth, relation); err == nil {
		t.Fatal("standalone negative acquired array provenance")
	}
	ordinaryElements := []typedef.DomainReplayNode{member(positive), member(positive), {Kind: "null"}, member(typedef.DomainReplayNode{Kind: "null"})}
	ordinary := array
	ordinary.Elements = &ordinaryElements
	if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("a", &ordinary), auth, relation); err == nil {
		t.Fatal("ordinary array acquired private provenance")
	}
	badDims := []typedef.DomainReplayDimension{{Lower: -2, Length: 5}}
	malformed := array
	malformed.Dimensions = &badDims
	if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("a", &malformed), auth, relation); err == nil {
		t.Fatal("inconsistent shape accepted")
	}
	wrongMembers := []typedef.DomainReplayMember{{Name: "other", Value: &negative}}
	wrongElements := append([]typedef.DomainReplayNode(nil), elements...)
	wrongElements[0] = typedef.DomainReplayNode{Kind: "composite", Members: &wrongMembers}
	malformed = array
	malformed.Elements = &wrongElements
	if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("a", &malformed), auth, relation); err == nil {
		t.Fatal("wrong member name accepted")
	}
	for _, text := range []string{"CAST('<(0,0),-1>' AS circle)", "circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", "pg_catalog.circle($1::pg_catalog.point,'-1'::pg_catalog.float8)", "pg_catalog.circle('(0,0)'::pg_catalog.point,(random())::pg_catalog.float8)"} {
		badElements := append([]typedef.DomainReplayNode(nil), elements...)
		badElements[0] = member(scalar(text))
		bad := array
		bad.Elements = &badElements
		if _, err := typecmds.CaptureDomainReplay(e, scope, source, record("a", &bad), auth, relation); err == nil {
			t.Fatal("invalid constructor leaf accepted", text)
		}
	}
	wrongPlaceholder := enginetest.Statement(t, "INSERT INTO public.conversion_replay_array VALUES(ARRAY[]::public.conversion_replay_member[],NULL)").(*parser.Insert)
	if _, err := typecmds.CaptureDomainReplay(e, scope, wrongPlaceholder, record("a", &array), auth, relation); err == nil {
		t.Fatal("nonNULL placeholder accepted")
	}
}

func domainUnitTable(t *testing.T, e *core.Engine, op *session.Operation, q string) *catalog.Relation {
	t.Helper()
	raw := enginetest.Statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = tablecmds.SchemaTableLocked(e, op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.MustRelation(addresses[0])
}
