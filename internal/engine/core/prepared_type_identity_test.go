package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTypeLifecyclePreparedCapture(t *testing.T) {
	// Exercise admitted executable casts only. Nominal-only companion graphs
	// are covered by generation closure and shared catalog/lifecycle scenarios.
	for _, family := range []struct {
		name, setup, extension string
		array                  bool
	}{
		{"mood", "CREATE TYPE public.mood AS ENUM ('a','b')", "", false},
		{"pair", "", "", false},
		{"pair", "", "", true},
		{"cube", "", "cube", false},
		{"query_int", "", "intarray", false},
		{"citext", "", "citext", false},
		{"ean13", "", "isn", false},
		{"isbn", "", "isn", false},
		{"isbn13", "", "isn", false},
		{"ismn", "", "isn", false},
		{"ismn13", "", "isn", false},
		{"issn", "", "isn", false},
		{"issn13", "", "isn", false},
		{"upc", "", "isn", false},
	} {
		array := family.array
		name := family.name
		if array {
			name += "_array"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			// Create each declaration with its own statement.
			if family.name == "pair" {
				enginetest.RoleExecute(t, e, op, "CREATE TYPE public.leaf AS ENUM ('a','b')")
				enginetest.RoleExecute(t, e, op, "CREATE TYPE public.pair AS(v leaf)")
			} else if family.extension != "" {
				enginetest.RoleExecute(t, e, op, "CREATE EXTENSION "+family.extension)
			} else {
				enginetest.RoleExecute(t, e, op, family.setup)
			}
			enginetest.RoleExecute(t, e, op, "CREATE SCHEMA identity_dst")
			old := domainUnitType(t, e.CatalogRead(), family.name)
			raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: family.name, Schema: &parser.Identifier{Text: "public"}, Pos: parser.Position{Offset: 11, Character: 12}}, Syntax: parser.PostfixCast}
			if array {
				raw.TypeName.ArrayBounds = []int32{-1}
			}
			first := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			if _, err := first.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
			key := analyze.PreparedTypeKey{Offset: 11, Kind: analyze.IdentityCast}
			captured := first.Prepared.Types[key]
			if captured.Base.Type == nil {
				t.Fatal("user type cast identity missing")
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := core.ScopeSelectPreparedAnalysis(scope, &parser.Select{}); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			analysis, err := core.EnginePreparedAnalysisLocked(e, scope, auth, analyze.Binding{Prepared: first.Prepared})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if family.name == "pair" {
				leaf := domainUnitType(t, e.CatalogRead(), "leaf")
				memberSeen := false
				for _, dependency := range core.PreparedAnalysisDependencies(analysis) {
					memberSeen = memberSeen || dependency.Ref.Address == leaf.Address()
				}
				if !memberSeen {
					t.Fatal("held composite lost executable member dependency")
				}
			}
			expected := core.NewDescription(core.DescriptionFields{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{captured.Base}}, CatalogOwner: e.CatalogRead().Owner(), CatalogEpoch: e.CatalogRead().Epoch(), Types: first.Prepared.Types, Analysis: analysis})
			enginetest.RoleExecute(t, e, op, "ALTER TYPE public."+family.name+" RENAME TO renamed")
			enginetest.RoleExecute(t, e, op, "ALTER TYPE public.renamed SET SCHEMA identity_dst")
			current := e.CatalogRead().MustType(old.Address())
			next := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			node, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || node.ResultUsage().Type.ID() != captured.Base.Type.ID() {
				t.Fatal("held cast lost current nominal generation", err)
			}
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			valid := core.EnginePreparedAnalysisValidLocked(e, auth, analysis, core.PreparedAnalysisPath(analysis))
			e.Mu.Unlock()
			if !valid || !catalog.SamePreparedTypeDefinition(old, current) {
				t.Fatal("identity-only edit retired held analysis")
			}
			changed := *current
			changed.Dimensions++
			if catalog.SamePreparedTypeDefinition(old, &changed) {
				t.Fatal("physical definition change concealed")
			}
			changed = *current
			changed.ACL.Explicit = !changed.ACL.Explicit
			if catalog.SamePreparedTypeDefinition(old, &changed) {
				t.Fatal("authority change concealed")
			}
			changed = *current
			changed.Owner = authorization.PrincipalRef{}
			if catalog.SamePreparedTypeDefinition(old, &changed) {
				t.Fatal("owner change concealed")
			}
			if current.SQLType.IsEan13() || current.SQLType.IsIsbn() || current.SQLType.IsIsbn13() || current.SQLType.IsIsmn() || current.SQLType.IsIsmn13() || current.SQLType.IsIssn() || current.SQLType.IsIssn13() || current.SQLType.IsUpc() {
				changed = *current
				changed.SQLType, err = sqltype.Citext(current.SQLType.ID(), current.Name(), current.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				if catalog.SamePreparedTypeDefinition(old, &changed) {
					t.Fatal("foreign installed family concealed")
				}
			}
			if current.SQLType.IsEnum() {
				changed = *current
				members := current.SQLType.EnumMembers()
				members[0].Label += "_changed"
				changed.SQLType, err = sqltype.EnumWithMembers(current.SQLType.ID(), current.Name(), current.SQLType.SQLName(), members)
				if err != nil {
					t.Fatal(err)
				}
				if catalog.SamePreparedTypeDefinition(old, &changed) {
					t.Fatal("enum definition change concealed")
				}
			}
			core.DescriptionRetireAnalysisTypeCasts(expected)
			next = newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			if _, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
				t.Fatal("retired occurrence rescued old spelling")
			}
		})
	}
}
