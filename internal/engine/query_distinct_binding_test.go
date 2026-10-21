package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryDistinctPhysicalDescriptorsAndRemap(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT id AS shown,v,id+1 AS computed FROM t WHERE FALSE")
	defer source.Close()
	schema := query.Root.Schema()
	if len(schema) != 3 || schema[0].Name != "shown" || schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 || !schema[0].NullabilityKnown || schema[0].Nullable || schema[1].OriginAttribute != 2 || !schema[1].NullabilityKnown || !schema[1].Nullable || schema[2].OriginTable != 0 || schema[2].OriginAttribute != 0 || schema[2].NullabilityKnown {
		t.Fatal(schema)
	}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	root, err := stored.RemapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	node := root.(*plan.ProjectNode).Child.(*plan.DistinctNode)
	for i, family := range node.Grouping {
		value, _ := sqltype.Integer(fresh[sqltype.Int4], 1)
		if _, err := family.Hash(context.Background(), value); err != nil {
			t.Fatal("remapped family retained obsolete generation", i, err)
		}
		old, _ := sqltype.Integer(schema[i].Usage.Type, 1)
		if _, err := family.Hash(context.Background(), old); err == nil {
			t.Fatal("old generation accepted", i)
		}
	}
}

func TestQueryDistinctSecondOutputOccurrence(t *testing.T) {
	for _, suffix := range []string{"y", "2"} {
		e, query, source := queryCoreFixture(t, "SELECT DISTINCT random() AS x,random() AS y FROM t ORDER BY "+suffix)
		project := query.Root.(*plan.ProjectNode)
		sorted := project.Child.(*plan.SortNode)
		node := sorted.Child.(*plan.DistinctNode)
		if len(node.Expressions) != 2 || len(node.Grouping) != 2 || sorted.Keys[0].Slot != 1 || sorted.Keys[0].OutputOrdinal != 2 {
			t.Fatal("second output occurrence lost", suffix, sorted.Keys, node)
		}
		column, ok := sorted.Keys[0].Expression.(plan.ColumnExpr)
		if !ok || column.Slot != 1 {
			t.Fatal("ORDER did not retain second projected datum", column, ok)
		}
		ast := statement(t, "CREATE VIEW occurrence_view AS SELECT DISTINCT random() AS x,random() AS y FROM t ORDER BY "+suffix)
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer done()
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			b, err := describe(op.Ctx, e.analysisHost(op), ast, auth, source.Ref, sqltype.InputEnvironment{}, nil, nil)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			sorted := b.Query.Root.(*plan.ProjectNode).Child.(*plan.SortNode)
			if sorted.Keys[0].Slot != 1 || sorted.Keys[0].OutputOrdinal != 2 {
				t.Fatal("view output occurrence lost", sorted.Keys)
			}
			view := ast.(*parser.CreateView)
			frozen, ok := view.Query.Order.Keys[0].Expr.(parser.LiteralExpr)
			if !ok || frozen.Value.Integer != 2 {
				t.Fatal("view froze wrong output ordinal", frozen, ok)
			}
		}()
		source.Close()
	}
}

func TestQueryDistinctInsertSourceDescriptorOwnership(t *testing.T) {
	for _, distinct := range []string{"DISTINCT", "DISTINCT ON(n)"} {
		for _, override := range []bool{false, true} {
			t.Run(distinct+"/"+map[bool]string{false: "assignment", true: "identity override"}[override], func(t *testing.T) {
				e := New()
				run(t, e, "CREATE TABLE src(n NUMERIC)")
				run(t, e, "INSERT INTO src VALUES(1.1),(1.1),(1.2),(1.2)")
				destination := "CREATE TABLE dst(id INT)"
				query := "INSERT INTO dst SELECT " + distinct + " n FROM src"
				if override {
					destination = "CREATE TABLE dst(id INT GENERATED ALWAYS AS IDENTITY)"
					query = "INSERT INTO dst OVERRIDING USER VALUE SELECT " + distinct + " n FROM src"
				}
				run(t, e, destination)
				op, done, err := testOperation(e, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := scope.Finish(nil); err != nil {
						t.Error(err)
					}
				}()
				ast := statement(t, query)
				if _, err = scope.Describe(ast); err != nil {
					t.Fatal(err)
				}
				e.mu.Lock()
				auth, err := e.authorizationLocked(op)
				if err != nil {
					e.mu.Unlock()
					t.Fatal(err)
				}
				b, err := describe(op.Ctx, e.analysisHost(op), ast, auth, scope.lockedRef, scope.input, nil, &scope.queryLockAdmission)
				if err != nil {
					e.mu.Unlock()
					t.Fatal(err)
				}
				view, err := scope.acquireDataViewLocked(e, b.QuerySources[0].Ref)
				e.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				source, err := exec.CaptureSource(op.Ctx, view, b.QuerySources[0].Ref)
				if err != nil {
					t.Fatal(err)
				}
				borrowQueryAuthority(source, scope)
				defer source.Close()
				project := b.Query.Root.(*plan.ProjectNode)
				node := project.Child
				var expressions []expr.Expr
				switch n := node.(type) {
				case *plan.DistinctNode:
					expressions = n.Expressions
				case *plan.DistinctOnNode:
					expressions = n.Expressions
				default:
					t.Fatalf("unexpected DISTINCT producer %T", node)
				}
				if project.Schema()[0].Usage.Type.ID() != sqltype.Int4 || node.Schema()[0].Usage.Type.ID() != sqltype.Numeric {
					t.Fatal("destination assignment mutated DISTINCT source schema", project.Schema(), node.Schema())
				}
				if next, discard := analyze.PruneIdentityQueryProducer(node, project.Expressions[0]); next != node || discard {
					t.Fatal("identity pruning crossed DISTINCT boundary", next, discard)
				}
				original := expressions[0]
				calls := 0
				expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
				rows, err := evaluateInsertQuery(op.Ctx, b, source, nil, "public", scope.input, scope.output, scope)
				if err != nil || len(rows.values) != 2 || calls != 4 || source.View != nil {
					t.Fatal(rows, calls, err, source.View)
				}
				for i, row := range rows.values {
					value, err := row[0].Integer()
					want := int64(1)
					if override {
						want = int64(i + 1)
					}
					if err != nil || value != want {
						t.Fatal(row, want, err)
					}
				}
			})
		}
	}
}

func TestQueryDistinctPhysicalACL(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE USER distinct_reader")
			roleExecute(t, e, op, "CREATE TABLE acl_t(id INT,secret INT)")
			if populated {
				roleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)")
			}
			reader, _ := e.Roles().Lookup("distinct_reader")
			grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			table := roleTableRef(t, e, "acl_t")
			id := table
			id.Address.SubID = 1
			grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
				t.Helper()
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant(table, 0)
			grant(id, authorization.SelectPrivilege)
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION distinct_reader")
			ast := statement(t, "SELECT DISTINCT id AS secret FROM acl_t ORDER BY secret")
			held, err := e.Describe(op, ast)
			if err != nil || len(held.Results) != 1 || held.Results[0].OriginAttribute != 1 {
				t.Fatal(held, err)
			}
			execute := func(denied bool) {
				t.Helper()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				scope.Expected = &held
				result, err := scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				if denied {
					assertDistinctACLFailure(t, result, err)
					return
				}
				want := 0
				if populated {
					want = 2
				}
				if err != nil || result.Rows == nil {
					t.Fatal(result, err)
				}
				if got := len(tuples(result)); got != want {
					result.Rows.Close()
					t.Fatal(got, want)
				}
				result.Rows.Close()
			}
			execute(false)
			for _, q := range []string{"SELECT DISTINCT secret FROM acl_t", "SELECT DISTINCT secret FROM acl_t WHERE FALSE", "SELECT DISTINCT id AS secret FROM acl_t WHERE secret=9"} {
				result, err := e.Execute(op, statement(t, q), 0, nil)
				assertDistinctACLFailure(t, result, err)
			}
			grant(id, 0)
			execute(true)
			result, err := e.Execute(op, statement(t, "SELECT DISTINCT 1 FROM acl_t WHERE FALSE"), 0, nil)
			assertDistinctACLFailure(t, result, err)
		})
	}
}

func assertDistinctACLFailure(t *testing.T, result Result, err error) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
	if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
		t.Fatal(result, got, err)
	}
}
