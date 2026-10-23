package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
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
	} {
		array := family.array
		name := family.name
		if array {
			name += "_array"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			// Stage each declaration independently, as the private staging seam
			// deliberately receives one parser statement rather than executing SQL.
			if family.name == "pair" {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.leaf AS ENUM ('a','b')"))
				domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE public.pair AS(v leaf)"))
			} else if family.extension != "" {
				domainUnitPublish(t, e, cubeUnitStage(t, e, op, family.extension, ""))
			} else {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, family.setup))
			}
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE SCHEMA identity_dst"))
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
			if err := scope.selectPreparedAnalysis(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			analysis, err := e.preparedAnalysisLocked(scope, auth, analyze.Binding{Prepared: first.Prepared})
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			if family.name == "pair" {
				leaf := domainUnitType(t, e.CatalogRead(), "leaf")
				memberSeen := false
				for _, dependency := range analysis.dependencies {
					memberSeen = memberSeen || dependency.Ref.Address == leaf.Address()
				}
				if !memberSeen {
					t.Fatal("held composite lost executable member dependency")
				}
			}
			expected := &StatementDescription{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{captured.Base}}, catalogOwner: e.CatalogRead().Owner(), catalogEpoch: e.CatalogRead().Epoch(), types: first.Prepared.Types, analysis: analysis}
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER TYPE public."+family.name+" RENAME TO renamed"))
			domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER TYPE public.renamed SET SCHEMA identity_dst"))
			current := e.CatalogRead().MustType(old.Address())
			next := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			node, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || node.ResultUsage().Type.ID() != captured.Base.Type.ID() {
				t.Fatal("held cast lost current nominal generation", err)
			}
			e.mu.Lock()
			auth, err = e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			valid := e.preparedAnalysisValidLocked(auth, analysis, analysis.path)
			e.mu.Unlock()
			if !valid || !samePreparedTypeDefinition(old, current) {
				t.Fatal("identity-only edit retired held analysis")
			}
			changed := *current
			changed.Dimensions++
			if samePreparedTypeDefinition(old, &changed) {
				t.Fatal("physical definition change concealed")
			}
			changed = *current
			changed.ACL.Explicit = !changed.ACL.Explicit
			if samePreparedTypeDefinition(old, &changed) {
				t.Fatal("authority change concealed")
			}
			changed = *current
			changed.Owner = authorization.PrincipalRef{}
			if samePreparedTypeDefinition(old, &changed) {
				t.Fatal("owner change concealed")
			}
			if current.SQLType.IsEan13() {
				changed = *current
				changed.SQLType, err = sqltype.Citext(current.SQLType.ID(), current.Name(), current.SQLType.SQLName())
				if err != nil {
					t.Fatal(err)
				}
				if samePreparedTypeDefinition(old, &changed) {
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
				if samePreparedTypeDefinition(old, &changed) {
					t.Fatal("enum definition change concealed")
				}
			}
			expected.retireAnalysisTypeCasts()
			next = newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			if _, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
				t.Fatal("retired occurrence rescued old spelling")
			}
		})
	}
}
