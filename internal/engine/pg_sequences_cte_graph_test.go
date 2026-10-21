package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func TestPGSequencesCTEStoredGraph(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE SEQUENCE a",
		"CREATE SEQUENCE b",
		"CREATE TABLE names(sequencename NAME)",
		"CREATE VIEW plain AS SELECT sequencename FROM names",
		"CREATE VIEW v AS SELECT sequencename FROM pg_sequences",
		"CREATE VIEW nested AS SELECT sequencename FROM v",
		"CREATE VIEW shared AS SELECT x.sequencename FROM v x JOIN v y ON x.sequencename=y.sequencename",
	} {
		sessionSQL(t, s, q)
	}
	catalog := e.CatalogRead()
	for _, name := range []string{"plain", "v", "nested", "shared"} {
		t.Run(name, func(t *testing.T) {
			root := &plan.SourceNode{Ref: catalogRef(t, e, catalog, name)}
			if analyze.CTEVolatile(root) {
				t.Fatal("stored source identity alone was classified as volatile")
			}
			volatile, err := stored.ViewVolatile(catalog, root)
			if err != nil || volatile != (name != "plain") {
				t.Fatalf("stored graph volatility=%v err=%v", volatile, err)
			}
			pgSequencesCTEHolder(t, e, s, false)
			lockEmpty(t, e)
		})
	}
	// The query graph walk also reaches a previously bound CTE or an unused
	// nested definition, whose stored source is not a newly bound query leaf.
	root := &plan.ProjectNode{Child: &plan.SingletonNode{}, AnalysisExpressions: []expr.Expr{
		plan.SubqueryExpr{QueryPlan: &plan.Query{Root: &plan.SourceNode{Ref: catalogRef(t, e, catalog, "nested")}}, CTEEdge: true},
	}}
	if volatile, err := stored.ViewVolatile(catalog, root); err != nil || !volatile {
		t.Fatalf("nested static definition volatility=%v err=%v", volatile, err)
	}
	pgSequencesCTEHolder(t, e, s, false)
	lockEmpty(t, e)

	// A fresh coherent catalog changes the answer without a mutable cache.
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT sequencename FROM names")
	catalog = e.CatalogRead()
	if volatile, err := stored.ViewVolatile(catalog, &plan.SourceNode{Ref: catalogRef(t, e, catalog, "nested")}); err != nil || volatile {
		t.Fatalf("replaced graph volatility=%v err=%v", volatile, err)
	}
}

func TestPGSequencesCTEStoredGraphAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE VIEW v AS SELECT sequencename FROM pg_sequences",
		"CREATE VIEW nested AS SELECT sequencename FROM v",
	} {
		sessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	root := &plan.SourceNode{Ref: catalogRef(t, e, view, "nested")}
	v := catalogRef(t, e, view, "v")
	broken := catalog.CloneState(view.State())
	broken.RemoveDescriptor(v.Address)
	if _, err := stored.ViewVolatile(e.catalog.read(broken), root); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("invalid stored identity err=%v want ErrCatalogReplaced", err)
	}

	// Publication continues to allow cycles; only expansion/static inspection
	// rejects the active path, using the existing view graph diagnostic.
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT sequencename FROM nested")
	view = e.CatalogRead()
	root = &plan.SourceNode{Ref: catalogRef(t, e, view, "nested")}
	_, err := stored.ViewVolatile(view, root)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P17" || diagnostic.Message != `infinite recursion detected in rules for relation "nested"` {
		t.Fatalf("cyclic stored graph err=%v want existing nested 42P17", err)
	}
	lockEmpty(t, e)
}
