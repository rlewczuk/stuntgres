package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryAliasesMetadata(t *testing.T) {
	for _, populated := range []bool{false, true} {
		e := New()
		run(t, e, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
		if populated {
			run(t, e, "INSERT INTO t VALUES(1,2,1)")
		}
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		base, err := e.Describe(op, statement(t, "SELECT * FROM t"))
		if err != nil || len(base.Results) != 3 || base.Results[0].OriginTable == 0 {
			done()
			t.Fatal(base, err)
		}
		typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
		usage := sqltype.Usage{Type: typ, Typmod: -1}
		origin := base.Results[0].OriginTable
		want := []catalog.Column{
			{Name: "shown", Usage: usage, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 1},
			{Name: "a", Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 2},
			{Name: "b", Usage: usage, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 3},
			{Name: "computed", Usage: usage},
		}
		ast := statement(t, "SELECT x.iid AS shown,x.a,x.b,x.iid+1 AS computed FROM t x(iid)")
		description, err := e.Describe(op, ast)
		if err != nil || !reflect.DeepEqual(description.Results, want) {
			done()
			t.Fatal(description.Results, want, err)
		}
		for _, prepared := range []bool{false, true} {
			scope, err := e.StartStatement(op)
			if err != nil {
				done()
				t.Fatal(err)
			}
			if prepared {
				scope.Expected = &description
			}
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if err != nil || result.Rows == nil {
				done()
				t.Fatal(result, err)
			}
			got := result.Rows.Schema()
			result.Rows.Close()
			if !reflect.DeepEqual(got, want) {
				done()
				t.Fatal(got, want)
			}
		}
		done()
	}
}

func TestQueryAliasesCancellation(t *testing.T) {
	for _, phase := range []string{"projection", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT x.iid AS shown FROM t x(iid) WHERE x.f")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			project := query.Root.(*plan.ProjectNode)
			calls := 0
			project.Expressions[0] = queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if phase == "projection" || calls == 2 {
					cancel()
				}
				return c.Row[0], nil
			})
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil || count != 0 || err != context.Canceled || source.View != nil || calls == 0 {
				t.Fatal(rows, count, err, source.View, calls)
			}
		})
	}
}

func TestQueryAliasesPhysicalACL(t *testing.T) {
	for _, populated := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		roleExecute(t, e, op, "CREATE USER alias_reader")
		roleExecute(t, e, op, "CREATE TABLE acl_t(id INT,secret INT)")
		if populated {
			roleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,9),(2,8)")
		}
		reader, _ := e.Roles().Lookup("alias_reader")
		grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
			t.Helper()
			if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
				t.Fatal(err)
			}
		}
		table := roleTableRef(t, e, "acl_t")
		grant(table, 0)
		id := table
		id.Address.SubID = 1
		grant(id, authorization.SelectPrivilege)
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION alias_reader")
		ast := statement(t, "SELECT x.iid AS secret FROM acl_t x(iid) ORDER BY secret")
		held, err := e.Describe(op, ast)
		if err != nil || len(held.Results) != 1 || held.Results[0].OriginAttribute != 1 || held.Results[0].Name != "secret" {
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
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
					t.Fatal(result, got, err, want)
				}
				return
			}
			if err != nil || result.Rows == nil {
				t.Fatal(result, err)
			}
			want := [][]driver.Value{}
			if populated {
				want = [][]driver.Value{{int64(1)}, {int64(2)}}
			}
			got := tuples(result)
			result.Rows.Close()
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
		}
		execute(false)
		for _, q := range []string{"SELECT x.secret FROM acl_t x(iid)", "SELECT x.iid AS secret FROM acl_t x(iid) WHERE secret=9"} {
			result, err := e.Execute(op, statement(t, q), 0, nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
			if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
				t.Fatal(q, result, got, err)
			}
		}
		grant(id, 0)
		execute(true)
		// An empty source and constant output must still require relation SELECT.
		result, err := e.Execute(op, statement(t, "SELECT 1 AS secret FROM acl_t x(iid) WHERE FALSE"), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
		if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
			t.Fatal(result, got, err)
		}
	}
}

func TestQueryAliasesTypmodAndCollation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	base, err := e.Describe(op, statement(t, "SELECT * FROM typed"))
	if err != nil || len(base.Results) != 2 || base.Results[0].OriginTable == 0 {
		t.Fatal(base, err)
	}
	varchar, _ := e.CatalogRead().LookupType(sqltype.VarChar)
	numeric, _ := e.CatalogRead().LookupType(sqltype.Numeric)
	origin := base.Results[0].OriginTable
	want := []catalog.Column{
		{Name: "label", Usage: sqltype.Usage{Type: varchar, Typmod: 9, Collation: sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}}, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 1},
		{Name: "price", Usage: sqltype.Usage{Type: numeric, Typmod: (7 << 16) + 2 + 4}, Nullable: true, NullabilityKnown: true, OriginTable: origin, OriginAttribute: 2},
	}
	for _, q := range []string{"SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)", "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount) WHERE FALSE"} {
		description, err := e.Describe(op, statement(t, q))
		if err != nil || !reflect.DeepEqual(description.Results, want) {
			t.Fatal(description.Results, want, err)
		}
	}
}

func TestQueryAliasesFailure(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid")
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	if rows != nil || count != 0 || source.View != nil || !errors.As(err, &got) || *got != want {
		t.Fatal(rows, count, source.View, got, err, want)
	}
}
