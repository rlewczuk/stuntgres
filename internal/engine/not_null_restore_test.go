package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func restoreNNName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}
func restoreNNText(text string) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}
}
func restoreNNEqual(column string, value parser.Expr) parser.Expr {
	return parser.OperatorExpr{Name: restoreNNName("="), Args: []parser.Expr{parser.ColumnExpr{Name: restoreNNName(column)}, value}}
}
func restoreNNRegclass(text string) parser.Expr {
	return parser.RegclassExpr{Child: restoreNNText(text)}
}
func restoreNNRaw(relation, column, name string) *parser.Update {
	query := &parser.Select{Targets: []parser.Expr{parser.ColumnExpr{Name: restoreNNName("attnum")}}, From: &parser.TableReference{Name: restoreNNName("pg_catalog", "pg_attribute")}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("attrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("attname", restoreNNText(column))}}
	return &parser.Update{Name: restoreNNName("pg_catalog", "pg_constraint"), Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "connoinherit"}, Value: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}}, Where: parser.LogicalExpr{Op: parser.LogicalAnd, Left: parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("conrelid", restoreNNRegclass(relation)), Right: restoreNNEqual("conname", restoreNNText(name))}, Right: restoreNNEqual("conkey", parser.ArrayExpr{Elements: []parser.Expr{parser.SubqueryExpr{Kind: parser.SubqueryScalar, Query: query}}})}}
}

func TestRestoreNotNullNoInheritSyntax(t *testing.T) {
	raw := restoreNNRaw(`public."quoted"`, "a", "captured")
	name, column, label, err := restoreNoInheritSyntax(raw)
	if err != nil || len(name.Parts) != 2 || name.Parts[1].Text != "quoted" || column != "a" || label != "captured" {
		t.Fatal(name, column, label, err)
	}
	mutations := map[string]func(*parser.Update){
		"schema":  func(r *parser.Update) { r.Name = restoreNNName("other", "pg_constraint") },
		"catalog": func(r *parser.Update) { r.Name = restoreNNName("pg_catalog", "pg_attribute") },
		"only":    func(r *parser.Update) { r.Only = true }, "star": func(r *parser.Update) { r.Star = true },
		"cte": func(r *parser.Update) { r.With = []parser.CommonTableExpression{{}} }, "recursive": func(r *parser.Update) { r.WithRecursive = true },
		"alias": func(r *parser.Update) { r.Alias.Text = "c" }, "from": func(r *parser.Update) { r.From = &parser.TableReference{Name: restoreNNName("p")} },
		"current_of": func(r *parser.Update) { r.CurrentOf = &parser.Identifier{Text: "cursor"} }, "returning": func(r *parser.Update) { r.Returning = &parser.Returning{} },
		"extra_assignment": func(r *parser.Update) { r.Assignments = append(r.Assignments, r.Assignments[0]) },
		"wrong_assignment": func(r *parser.Update) { r.Assignments[0].Target.Text = "conislocal" },
		"target_path":      func(r *parser.Update) { r.Assignments[0].Steps = []parser.TargetStep{{}} },
		"false": func(r *parser.Update) {
			r.Assignments[0].Value = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue}}
		},
		"parameter": func(r *parser.Update) { r.Assignments[0].Value = parser.ParameterExpr{Number: 1} },
		"predicate_or": func(r *parser.Update) {
			where := r.Where.(parser.LogicalExpr)
			where.Op = parser.LogicalOr
			r.Where = where
		},
		"missing_key": func(r *parser.Update) { r.Where = r.Where.(parser.LogicalExpr).Left },
		"empty_key": func(r *parser.Update) {
			where := r.Where.(parser.LogicalExpr)
			where.Right = restoreNNEqual("conkey", parser.ArrayExpr{})
			r.Where = where
		},
		"unqualified_relation": func(r *parser.Update) {
			where := r.Where.(parser.LogicalExpr)
			pair := where.Left.(parser.LogicalExpr)
			pair.Left = restoreNNEqual("conrelid", restoreNNRegclass("p"))
			where.Left = pair
			r.Where = where
		},
	}
	for _, field := range []string{"bounds", "group", "different_relation", "wrong_attribute_catalog", "wrong_target", "alias", "exists", "parameter"} {
		field := field
		mutations["subquery_"+field] = func(r *parser.Update) {
			where := r.Where.(parser.LogicalExpr)
			key := where.Right.(parser.OperatorExpr)
			array := key.Args[1].(parser.ArrayExpr)
			subquery := array.Elements[0].(parser.SubqueryExpr)
			query := subquery.Query
			switch field {
			case "bounds":
				query.Bounds = &parser.QueryBounds{}
			case "group":
				query.GroupBy = &parser.GroupClause{}
			case "different_relation":
				query.Where = parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("attrelid", restoreNNRegclass("public.other")), Right: restoreNNEqual("attname", restoreNNText("a"))}
			case "wrong_attribute_catalog":
				query.From = &parser.TableReference{Name: restoreNNName("pg_catalog", "pg_class")}
			case "wrong_target":
				query.Targets = []parser.Expr{parser.ColumnExpr{Name: restoreNNName("attname")}}
			case "alias":
				query.Labels = []parser.Identifier{{Text: "renamed"}}
			case "exists":
				subquery.Kind = parser.SubqueryExists
			case "parameter":
				query.Where = parser.LogicalExpr{Op: parser.LogicalAnd, Left: restoreNNEqual("attrelid", restoreNNRegclass("public.p")), Right: restoreNNEqual("attname", parser.ParameterExpr{Number: 1})}
			}
			array.Elements[0] = subquery
			key.Args[1] = array
			where.Right = key
			r.Where = where
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			raw := restoreNNRaw("public.p", "a", "captured")
			mutate(raw)
			_, _, _, err := restoreNoInheritSyntax(raw)
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: restoreNoInheritBoundary}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("noncanonical repair admitted or wrong diagnostic", diagnostic, err)
			}
		})
	}
}

func TestRestoreNotNullNoInheritAuthority(t *testing.T) {
	e := New().restoreStage()
	old := inheritanceUnitRelation(e, "p", 22000, "a")
	old.Columns[0].Nullable = false
	old.NotNullConstraints = []notNullConstraint{{OID: 23000, Name: "captured", Attribute: 1}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(old); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	old = e.CatalogRead().MustRelation(old.Address())
	op, done := roleOperation(t, e, "postgres")
	defer done()
	origin := restoredNotNull{ref: e.catalog.Domain.Ref(e.CatalogRead().Epoch(), old.Address()), attribute: 1, constraint: old.NotNullConstraints[0].OID}
	raw := restoreNNRaw("public.p", "a", "captured")
	for name, mutate := range map[string]func(*restoredNotNull){"epoch": func(r *restoredNotNull) { r.ref.Epoch++ }, "attribute": func(r *restoredNotNull) { r.attribute++ }, "constraint": func(r *restoredNotNull) { r.constraint++ }, "missing_pair": func(r *restoredNotNull) { *r = restoredNotNull{} }} {
		t.Run(name, func(t *testing.T) {
			candidate := origin
			mutate(&candidate)
			before := e.catalog.state
			if err := e.restoreNoInheritNotNull(op, raw, candidate); err == nil || e.catalog.state != before {
				t.Fatal("foreign pairing published", err)
			}
		})
	}
	for _, kind := range []string{"invalid", "nullable", "inherited", "primary"} {
		before := e.catalog.state
		altered := *old
		altered.Columns = append([]ColumnDescriptor(nil), old.Columns...)
		altered.NotNullConstraints = append([]notNullConstraint(nil), old.NotNullConstraints...)
		switch kind {
		case "invalid":
			altered.NotNullConstraints[0].NotValid = true
		case "nullable":
			altered.Columns[0].Nullable = true
		case "inherited":
			altered.Inheritance = &tableInheritance{NotNull: map[int32]inheritedConstraint{1: {Origins: []inheritanceOrigin{{Parent: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22001}, Attribute: 1, Constraint: 23001}}}}}
		case "primary":
			altered.UniqueConstraints = []uniqueConstraint{{OID: 23002, Name: "key", Primary: true, Attributes: []int32{1}}}
		}
		candidate := cloneCatalog(before)
		candidate.SetDescriptor(old.Address(), &altered)
		e.catalog.state = candidate
		if err := e.restoreNoInheritNotNull(op, raw, origin); err == nil || e.catalog.state != candidate {
			t.Fatal("ineligible NN fact published", kind, err)
		}
		e.catalog.state = before
	}
	targets := e.importTargets
	e.importTargets = nil
	before := e.catalog.state
	if err := e.restoreNoInheritNotNull(op, raw, origin); err == nil || e.catalog.state != before {
		t.Fatal("ordinary engine gained importer authority", err)
	}
	e.importTargets = targets
	foreign := New()
	foreignOp, foreignDone := roleOperation(t, foreign, "postgres")
	defer foreignDone()
	if err := e.restoreNoInheritNotNull(foreignOp, raw, origin); err == nil || e.catalog.state != before {
		t.Fatal("foreign operation gained importer authority", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOp, cancelDone, err := testOperation(e, canceled)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelDone()
	cancel()
	if err := e.restoreNoInheritNotNull(cancelOp, raw, origin); !errors.Is(err, context.Canceled) || e.catalog.state != before {
		t.Fatal("canceled repair published", err)
	}
	if err := e.restoreNoInheritNotNull(op, raw, origin); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().MustRelation(old.Address())
	if !current.NotNullConstraints[0].NoInherit || old.NotNullConstraints[0].NoInherit || current.NotNullConstraints[0].OID != origin.constraint || current.Columns[0].Nullable || current.Revision() != old.Revision()+1 {
		t.Fatal("repair changed identity/local nullability or old capture", current)
	}
}
