package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

func TestDropClosureRetainedQueryAuthority(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "projected_attribute", true: "retired_provider"}[retired], func(t *testing.T) {
			e := engine.New()
			for _, sql := range []string{
				"CREATE DOMAIN closure_integrity_domain AS integer",
				"CREATE TABLE closure_integrity_source(v closure_integrity_domain)",
				"CREATE VIEW closure_integrity_view AS SELECT v FROM closure_integrity_source",
			} {
				enginetest.Run(t, e, sql)
			}
			base := e.CatalogRead()
			source := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_source").Address)
			dependent := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_view").Address)
			domain := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[0].Usage.Type.ID())}
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if retired {
				if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), source.Address())); err != nil {
					t.Fatal(err)
				}
			} else {
				projected := *source
				projected.Columns, projected.Attributes = nil, nil
				if err := stage.Replace(&projected); err != nil {
					t.Fatal(err)
				}
			}
			reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
			before := stage.RemovalCount()
			passive, err := view.PassiveTypeDependency(reader, dependent, domain)
			if err != nil || !passive {
				t.Fatal("retained passive query lost entering authority", passive, err)
			}
			if stage.RemovalCount() != before || base.MustRelation(source.Address()) != source {
				t.Fatal("classification mutated retirement or original provider")
			}
		})
	}
}

func TestDropClosureNamedTypeDefaultDiagnostic(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE TYPE closure_integrity_type AS (a integer)",
		"CREATE FUNCTION closure_integrity_default(x integer DEFAULT (NULL::closure_integrity_type).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	relation := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_integrity_type").Address)
	typ := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, address := range []ids.ObjectAddress{typ, relation.Address()} {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	roots := []ids.ObjectAddress{typ, relation.Address()}
	edges, err := view.DropClosure(reader, roots)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic *sqlstate.Error
	if !errors.As(view.RestrictError(reader, roots, edges, 1, ids.ObjectAddress{}), &diagnostic) {
		t.Fatal("missing actual RESTRICT diagnostic")
	}
	want := "function closure_integrity_default(integer) depends on type closure_integrity_type"
	if diagnostic.Code != "2BP01" || diagnostic.Detail != want {
		t.Fatal("named type diagnostic was replaced by backing relation", diagnostic)
	}
	// Prepared retirement can discover the default through the backing
	// attribute before its whole-relation carrier. The SQL DROP subject is
	// still the named type, never the backing composite relation or field.
	attribute := relation.Address()
	attribute.SubID = relation.Attributes[0]
	edges, err = view.DropClosure(reader, []ids.ObjectAddress{attribute})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.As(view.RestrictError(reader, []ids.ObjectAddress{attribute}, edges, 1, typ), &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Detail != want {
		t.Fatal("named type subject lost through backing attribute", diagnostic)
	}
}
