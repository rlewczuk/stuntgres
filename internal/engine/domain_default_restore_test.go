package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDomainDefaultReplayAdmissionInternal(t *testing.T) {
	e := New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.replay_required AS integer NOT NULL"))
	domainUnitTable(t, e, op, "CREATE TABLE public.replay_domain(v public.replay_required,w integer)")
	source := statement(t, "INSERT INTO public.replay_domain(v,w) VALUES(NULL,1)").(*parser.Insert)
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
	e.Mu.Lock()
	ref, err := e.ResolveRelationReference(auth, source.Name, false)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		t.Fatal("missing destination")
	}
	identity := []string{"public", "replay_required"}
	valid := typedef.DomainReplayRecord{Schema: "public", Table: "replay_domain", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "domain_null", Type: &identity}}}}
	capture, err := CaptureDomainReplay(e, scope, source, valid, auth, relation)
	if err != nil || capture == nil || len(capture.Cells) != 1 || !capture.Cells[0].Value.IsNull() || capture.Cells[0].Value.Type() != relation.Columns[0].Usage.Type {
		t.Fatal("exact domain NULL admission", capture, err)
	}
	for _, query := range []string{
		"INSERT INTO public.replay_domain(v,w) VALUES(CAST(NULL AS public.replay_required),1)",
		"INSERT INTO public.replay_domain(w) VALUES(NULL)",
		"INSERT INTO public.replay_domain(v,v) VALUES(NULL,NULL)",
	} {
		raw := statement(t, query).(*parser.Insert)
		if _, err := CaptureDomainReplay(e, scope, raw, valid, auth, relation); err == nil {
			t.Fatal("invalid destination admitted", query)
		}
	}
	ordinary := valid
	ordinary.Cells = []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "null"}}}
	if _, err := CaptureDomainReplay(e, scope, source, ordinary, auth, relation); err == nil {
		t.Fatal("fresh NULL acquired same-domain provenance")
	}
	foreign := valid
	wrong := []string{"other", "replay_required"}
	foreign.Cells = []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "domain_null", Type: &wrong}}}
	if _, err := CaptureDomainReplay(e, scope, source, foreign, auth, relation); err == nil {
		t.Fatal("foreign identity admitted")
	}
	forged := *relation
	if _, err := CaptureDomainReplay(e, scope, source, valid, auth, &forged); err == nil {
		t.Fatal("noncatalog destination admitted")
	}
	// A legitimate session, superuser and AST do not grant native import authority.
	normal := New()
	normalOp, normalDone := roleOperation(t, normal, "postgres")
	defer normalDone()
	normalAuth, err := core.OperationAuthorization(normalOp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureDomainReplay(normal, &core.StatementScope{Op: normalOp}, source, valid, normalAuth, nil); err == nil {
		t.Fatal("ordinary engine acquired native replay authority")
	}
}

func TestDomainDefaultReplayDeferredChecksInternal(t *testing.T) {
	e := New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	// Restore adds captured CHECK recipes after rows, so the live declared
	// domain legitimately has no constraints at this exact import position.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.replay_deferred AS integer"))
	table := domainUnitTable(t, e, op, "CREATE TABLE public.deferred_rows(v public.replay_deferred)")
	source := statement(t, "INSERT INTO public.deferred_rows VALUES(NULL)").(*parser.Insert)
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
	identity := []string{"public", "replay_deferred"}
	record := typedef.DomainReplayRecord{Schema: "public", Table: "deferred_rows", Cells: []typedef.DomainReplayCell{{Column: "v", Value: &typedef.DomainReplayNode{Kind: "domain_null", Type: &identity}}}}
	capture, err := CaptureDomainReplay(e, scope, source, record, auth, table)
	if err != nil {
		t.Fatal("deferred CHECK declaration lost existing domain NULL provenance", err)
	}
	if value := capture.Cells[0].Value; !value.IsNull() || value.Type() != table.Columns[0].Usage.Type {
		t.Fatal("lost authentic declared domain identity", value)
	}
}

func TestDomainDefaultReplayRoutineManifestInternal(t *testing.T) {
	e := New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	text := "pg_catalog.timezone('UTC'::text,'-infinity'::timestamptz)"
	source := statement(t, "SELECT "+text)
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
	typ, ok := auth.Catalog.LookupType(sqltype.Timestamp)
	if !ok {
		t.Fatal("missing timestamp")
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	value, count, err := typedef.RestoreDomainReplayNode(DomainReplayEnv(e, &c, op, auth), typ.Usage(), &typedef.DomainReplayNode{Kind: "scalar", Scalar: &text})
	if err != nil || count != 0 {
		t.Fatal("closed endpoint expression lost its original binding manifest", count, err)
	}
	output, err := typ.OutputWithEnvironment(op.Context(), value, scope.Output)
	if err != nil || output != "-infinity" {
		t.Fatal(output, err)
	}
	// The leaf's local original manifest must not leak into its enclosing
	// domain CHECK recipe or authorize a later unrelated expression.
	if c.RoutineRecipe || c.RoutineChecks != nil || c.RoutineReferences != nil {
		t.Fatal("scalar lifetime escaped its local evaluation")
	}
}
