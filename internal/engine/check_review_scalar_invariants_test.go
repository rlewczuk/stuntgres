package engine

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestCheckReviewScalarInquiryStoredInvariants(t *testing.T) {
	ctx := context.Background()
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a OID CONSTRAINT c CHECK(col_description(a,1) IS NULL))")
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "t")
	relation := view.MustRelation(ref.Address)
	if !relation.checkConstraints[0].commentSnapshot {
		t.Fatal("bound inquiry lost snapshot demand")
	}
	badRelation := *relation
	badRelation.checkConstraints = append([]checkConstraint(nil), relation.checkConstraints...)
	badRelation.checkConstraints[0].commentSnapshot = false
	if err := validateChecks(view, &badRelation, map[string]bool{}); err == nil {
		t.Fatal("invalid snapshot demand persisted")
	}
	inquiry := relation.checkConstraints[0].expression.(nullTestExpression).child.(commentInquiryExpression)
	intType, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4 type")
	}
	renderer := scalarRenderer{owner: relation}
	if _, err := renderer.sql(ctx, view, inquiry); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultSQL(ctx, view, inquiry); err == nil {
		t.Fatal("DEFAULT admitted comment inquiry")
	}
	for _, test := range []struct {
		name   string
		change func(*commentInquiryExpression)
	}{
		{"kind", func(n *commentInquiryExpression) { n.kind = commentInquiryKind(255) }},
		{"arity", func(n *commentInquiryExpression) { n.args = n.args[:1] }},
		{"missing-child", func(n *commentInquiryExpression) { n.args[0] = nil }},
		{"argument-type", func(n *commentInquiryExpression) { n.args[0] = n.args[1] }},
		{"result-type", func(n *commentInquiryExpression) { n.Usage = intType.Usage() }},
		{"properties", func(n *commentInquiryExpression) { n.Property.RowDependent = false }},
		{"foreign-origin", func(n *commentInquiryExpression) {
			column := n.args[0].(columnExpression)
			column.origin.Epoch++
			n.args[0] = column
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := inquiry
			bad.args = append([]expr.Expr(nil), inquiry.args...)
			test.change(&bad)
			if _, err := renderer.sql(ctx, view, bad); err == nil {
				t.Fatal("malformed inquiry persisted")
			}
		})
	}
}

func TestCheckReviewScalarInquiryRowArgumentReplacement(t *testing.T) {
	for _, expr := range []string{"obj_description(a,'pg_class')", "obj_description(a)", "col_description(a,1)", "shobj_description(a,'pg_authid')"} {
		for _, self := range []bool{false, true} {
			name := "foreign/" + expr
			if self {
				name = "self/" + expr
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				source := New()
				s := sessionStart(t, source, nil)
				sessionSQL(t, s, "CREATE TABLE t(a OID CONSTRAINT c CHECK("+expr+" IS NULL))")
				sessionSQL(t, s, "INSERT INTO t VALUES(0)")
				before := source.CatalogRead()
				var dump bytes.Buffer
				if err := source.Dump(ctx, &dump); err != nil {
					t.Fatal(err)
				}
				target := New()
				peer := sessionStart(t, target, nil)
				if self {
					target, peer = source, s
				}
				sessionSQL(t, peer, "CREATE TEMP SEQUENCE kept START 30")
				sequenceScalar(t, peer, "SELECT nextval('kept')", 30)
				_, description := sequencePreparedDescription(t, target, peer, "SELECT nextval('kept')")
				tempRef := description.sequences.constants[0].ref
				temp := target.catalog.state.Get(tempRef.Address)
				if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
					t.Fatal(err)
				}
				if target.catalog.state.Get(tempRef.Address) != temp {
					t.Fatal("replacement changed retained TEMP descriptor")
				}
				sessionSQL(t, peer, "UPDATE t SET a=0")
				var again bytes.Buffer
				if err := target.Dump(ctx, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
					t.Fatal("row-dependent inquiry changed on replacement", err)
				}
				if err := target.Restore(ctx, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				sessionSQL(t, peer, "INSERT INTO t VALUES(0)")
				sequenceScalar(t, peer, "SELECT nextval('kept')", 31)
			})
		}
	}
}

func TestCheckReviewScalarInquiryStatementSnapshot(t *testing.T) {
	ctx := context.Background()
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE anchor(a INT)")
	sessionSQL(t, s, "COMMENT ON TABLE anchor IS 'old'")
	sessionSQL(t, s, "CREATE TABLE t(a TEXT CONSTRAINT c CHECK(obj_description('anchor'::regclass,'pg_class') IS NOT DISTINCT FROM a))")
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "t")
	relation := view.MustRelation(ref.Address)
	inquiry := relation.checkConstraints[0].expression.(distinctExpression).left
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	scope.routineAuth = auth
	e.captureCheckCommentSnapshotLocked(scope, relation)
	catalog, roles := scope.commentCatalog, scope.commentRoles
	e.mu.Unlock()
	sessionSQL(t, peer, "COMMENT ON TABLE anchor IS 'new'")
	e.mu.Lock()
	// A later query-source opening shares, rather than replaces, this pair.
	source, captureErr := e.captureQuerySourcesLocked(scope, nil, true)
	e.mu.Unlock()
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	defer source.close()
	if scope.commentCatalog != catalog || scope.commentRoles != roles {
		t.Fatal("query opening replaced the statement comment snapshot")
	}
	query := queryState{source: source, input: scope.input, output: scope.output}
	evaluation := query.evaluation(ctx)
	evaluation.routineRecipe = true
	evaluation.routineChecks = relation.checkConstraints[0].dependencies
	evaluation.routineConsumer = relation
	if err := admitRoutineCall(&evaluation, inquiry, true, false); err != nil {
		t.Fatal(err)
	}
	value, err := inquiry.Eval(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Text()
	if err != nil || text != "old" {
		t.Fatal("inquiry did not retain its immutable snapshot", text, err)
	}
}

func TestCheckReviewScalarNoInquirySnapshotDemand(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))")
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "t")
	relation := view.MustRelation(ref.Address)
	scope := &StatementScope{}
	e.mu.Lock()
	e.captureCheckCommentSnapshotLocked(scope, relation)
	e.mu.Unlock()
	if scope.commentCatalog.state != nil || scope.deparseAuth.roleRecord != nil {
		t.Fatal("unrelated CHECK captured comment metadata")
	}
}
