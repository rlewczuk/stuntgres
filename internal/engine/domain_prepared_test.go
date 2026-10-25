package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDomainIdentityPreparedCapture(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE DOMAIN public.d AS integer")
	old := domainUnitType(t, e.CatalogRead(), "d")
	raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1, Pos: parser.Position{Offset: 7, Character: 8}}, TypeName: parser.TypeName{Name: "d", Schema: &parser.Identifier{Text: "public"}, Pos: parser.Position{Offset: 11, Character: 12}}, Syntax: parser.PostfixCast}
	first := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	if _, err := first.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
		t.Fatal(err)
	}
	key := analyze.PreparedTypeKey{Offset: 11, Kind: analyze.DomainCast}
	if first.Prepared.Types[key].Base.Type != old.SQLType {
		t.Fatal("cast source identity not captured")
	}
	expected := &StatementDescription{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{old.SQLType.Usage()}}, catalogOwner: e.CatalogRead().Owner(), catalogEpoch: e.CatalogRead().Epoch(), types: first.Prepared.Types, analysis: &preparedAnalysis{}}
	roleExecute(t, e, op, "ALTER DOMAIN public.d RENAME TO renamed")
	current := e.CatalogRead().MustType(old.Address())
	next := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	node, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil || node.ResultUsage().Type != current.SQLType || next.Prepared.Types[key].Base.Type != current.SQLType {
		t.Fatal("held cast missed admitted same-ID generation", err)
	}
	// A sibling source occurrence must not borrow another cast's identity.
	sibling := raw
	sibling.TypeName.Pos = parser.Position{Offset: 29, Character: 30}
	annotation, err := next.TypeCastAnnotation(sibling)
	if err != nil || annotation.Name != "d" {
		t.Fatal("sibling retargeted", annotation, err)
	}
	// Owner/epoch mismatch never turns the old ID into current authority.
	foreign := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, expected)
	annotation, err = foreign.TypeCastAnnotation(raw)
	if err != nil || annotation.Name != "d" {
		t.Fatal("foreign catalog reused old ID", annotation, err)
	}
	expected.retireAnalysisTypeCasts()
	if !expected.analysis.needsReanalysis {
		t.Fatal("failed reanalysis could revive captures when path restored")
	}
	next = newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	if _, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
		t.Fatal("retired old spelling was rescued")
	}
}

func TestDomainIdentityArrayCapture(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "bracketed"
		if direct {
			name = "companion"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE DOMAIN public.d AS integer")
			roleExecute(t, e, op, "CREATE SCHEMA identity_dst")
			old := domainUnitType(t, e.CatalogRead(), "d")
			oldArray := e.CatalogRead().MustType(old.Address()).Array
			raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "d", Schema: &parser.Identifier{Text: "public"}, Pos: parser.Position{Offset: 12, Character: 13}, ArrayBounds: []int32{-1, -1}}, Syntax: parser.PostfixCast}
			if direct {
				raw.TypeName.Name, raw.TypeName.ArrayBounds = "_d", nil
			}
			first := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			if _, err := first.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
				t.Fatal(err)
			}
			key := analyze.PreparedTypeKey{Offset: 12, Kind: analyze.DomainCast}
			captured := first.Prepared.Types[key]
			if captured.Base.Type == nil || captured.Base.Type.ID() != oldArray || !captured.Base.Type.Element().IsDomain() {
				t.Fatal("companion cast occurrence not captured")
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.selectPreparedAnalysis(&parser.Select{}); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			analysis, err := e.preparedAnalysisLocked(scope, auth, analyze.Binding{Prepared: first.Prepared})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			arraySeen, elementSeen := false, false
			for _, dep := range analysis.dependencies {
				arraySeen = arraySeen || sqltype.ID(dep.Ref.Address.ObjectID) == oldArray
				elementSeen = elementSeen || dep.Ref.Address == old.Address()
			}
			if !arraySeen || !elementSeen {
				t.Fatal("held companion lost carrier or executable element dependency")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			valid := func() bool {
				t.Helper()
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					t.Fatal(err)
				}
				return e.preparedAnalysisValidLocked(auth, analysis, analysis.path)
			}
			expected := &StatementDescription{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{captured.Base}}, catalogOwner: e.CatalogRead().Owner(), catalogEpoch: e.CatalogRead().Epoch(), types: first.Prepared.Types, analysis: analysis}
			roleExecute(t, e, op, `ALTER DOMAIN public.d RENAME TO "renamed array domain"`)
			roleExecute(t, e, op, `ALTER DOMAIN public."renamed array domain" SET SCHEMA identity_dst`)
			current := e.CatalogRead().MustType(old.Address())
			next := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			annotation, err := next.TypeCastAnnotation(raw)
			wantName := current.Name()
			if direct {
				array, ok := e.CatalogRead().LookupArray(current.SQLType)
				if !ok {
					t.Fatal("current reciprocal companion missing")
				}
				wantName = array.Name()
			}
			if err != nil || annotation.Name != wantName || annotation.Schema == nil || annotation.Schema.Text != "identity_dst" || len(annotation.ArrayBounds) != len(raw.TypeName.ArrayBounds) || annotation.Pos != raw.TypeName.Pos {
				t.Fatal("wrong companion annotation form", annotation, err)
			}
			node, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil || node.ResultUsage().Type.ID() != oldArray {
				t.Fatal("held array cast lost its carrier", err)
			}
			if next.Prepared.Types[key].Base.Type.Element() != current.SQLType {
				t.Fatal("held companion retained an obsolete element generation")
			}
			if !valid() {
				t.Fatal("array identity edits unnecessarily retired held analysis")
			}
			usingIndexUnitExecute(t, e, sessionStart(t, e, nil), statement(t, `ALTER DOMAIN identity_dst."renamed array domain" ADD CONSTRAINT positive CHECK(VALUE>0)`))
			if valid() {
				t.Fatal("stable array identity concealed changed element semantics")
			}
			foreign := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, expected)
			annotation, err = foreign.TypeCastAnnotation(raw)
			if err != nil || annotation.Name != raw.TypeName.Name {
				t.Fatal("foreign owner reused array occurrence", annotation, err)
			}
			expected.retireAnalysisTypeCasts()
			next = newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
			if _, err := next.Cast(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
				t.Fatal("retired array occurrence rescued old spelling")
			}
		})
	}
}
