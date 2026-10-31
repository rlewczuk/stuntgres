package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIdentityOwnCheckBindingAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`)
	read := e.CatalogRead()
	table := read.MustRelation(roleTableRef(t, e, "i").Address)
	address := table.ColumnMeta.Identities[table.Attributes[0]].Sequence
	original := read.MustRelation(address)
	stage := e.BeginCatalogStage(read)
	candidate := *original
	candidate.ObjectName = "candidate_only"
	candidate.SequencePlanStamp++
	if err := stage.Replace(&candidate); err != nil {
		t.Fatal(err)
	}
	namespace := *read.MustNamespace(candidate.Namespace())
	namespace.ObjectName = "candidate_schema"
	namespace.ObjectRevision++
	if err := stage.Replace(&namespace); err != nil {
		t.Fatal(err)
	}
	ast := enginetest.Statement(t, `SELECT pg_catalog.nextval('candidate_only')`).(*parser.Select)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	// Search paths resolve lexical schema names against the candidate too.
	// The real public namespace now has only its private candidate_schema name.
	path, err := e.Settings.Definitions()["search_path"].Parse("candidate_schema")
	if err != nil {
		t.Fatal(err)
	}
	core.AuthorizationSettings(&auth).Mu.Lock()
	core.AuthorizationSettings(&auth).Typed["search_path"] = path
	core.AuthorizationSettings(&auth).Values["search_path"] = "candidate_schema"
	core.AuthorizationSettings(&auth).Mu.Unlock()
	auth.Catalog = e.StageRead(stage)
	auth.Recapture(auth.Catalog)
	binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	binder.Query.Auth = &auth
	if _, err := functions.BindSequenceCall(binder, ast.Targets[0].(parser.CallExpr)); err != nil {
		t.Fatal(err)
	}
	if len(binder.Prepared.SequenceConstants) != 1 {
		t.Fatal(binder.Prepared.SequenceConstants)
	}
	constant := binder.Prepared.SequenceConstants[0]
	if constant.Ref.Address != address || constant.Ref.Domain() != e.Catalog.Domain || constant.Ref.Epoch != read.Epoch() || constant.Stamp != candidate.SequencePlanStamp {
		t.Fatal("regclass resolution/stamp mixed candidate and published authority", constant)
	}
	qualified := enginetest.Statement(t, `SELECT candidate_schema.nextval('candidate_only')`).(*parser.Select).Targets[0].(parser.CallExpr)
	if visible, err := binder.BuiltinVisible(qualified.Name); err != nil || visible {
		t.Fatal("builtin namespace lookup ignored candidate authority", visible, err)
	}
	if e.Catalog.State != read.State() || e.Catalog.State.Get(address) != original || e.Storage.Sequences[address].Called {
		t.Fatal("binding published or evaluated the candidate")
	}
}

func checkReviewDependencyValid(t *testing.T, e *core.Engine) {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, c := range d.Constraints.Check {
			for _, dep := range c.Dependencies {
				if err := enginetest.ValidateObjectRef(e, read, dep.Ref); err != nil {
					t.Fatalf("orphan CHECK %s on %s: %v", c.Name, d.Name(), err)
				}
			}
		}
	}
	var dump strings.Builder
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal("unusable catalog", err)
	}
}
