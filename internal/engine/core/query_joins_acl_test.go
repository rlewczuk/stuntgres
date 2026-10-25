package core_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestQueryJoinsEverySourceACL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			query    string
			required int32
		}{
			{"SELECT 1 FROM a CROSS JOIN b", 0},
			{"SELECT count(*) FROM a CROSS JOIN b WHERE FALSE", 0},
			{"SELECT a.k FROM a JOIN b ON a.k=b.k", 1},
			{"SELECT a.k FROM a JOIN b USING(k)", 1},
			{"SELECT u.k FROM a JOIN b USING(k) AS u", 1},
			{"SELECT a.k FROM a NATURAL JOIN b", 2},
			{"SELECT a.k FROM a CROSS JOIN b WHERE b.v=1", 2},
			{"SELECT a.ak FROM a AS a(ak,av) CROSS JOIN b ORDER BY v", 2},
			{"SELECT count(*) FROM a CROSS JOIN b GROUP BY b.v HAVING b.v=1", 2},
			{"SELECT 1 FROM a LEFT JOIN b ON FALSE LIMIT 0", 0},
			{"SELECT 1 FROM a RIGHT JOIN b ON FALSE LIMIT 0", 0},
			{"SELECT 1 FROM a FULL JOIN b ON FALSE LIMIT 0", 0},
			{"SELECT 1 FROM a LEFT JOIN b USING(k) LIMIT 0", 1},
			{"SELECT 1 FROM a RIGHT JOIN b USING(k) LIMIT 0", 1},
			{"SELECT 1 FROM a FULL JOIN b USING(k) LIMIT 0", 1},
			{"SELECT a.k FROM a NATURAL LEFT JOIN b LIMIT 0", 2},
			{"SELECT a.k FROM a NATURAL RIGHT JOIN b LIMIT 0", 2},
			{"SELECT a.k FROM a NATURAL FULL JOIN b LIMIT 0", 2},
		} {
			t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE USER join_reader", "CREATE TABLE a(k INT,v INT)", "CREATE TABLE b(k INT,v INT)", "INSERT INTO a VALUES(1,1)"} {
					enginetest.RoleExecute(t, e, op, q)
				}
				reader, _ := e.Roles().Lookup("join_reader")
				grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
					t.Helper()
					if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(enginetest.RoleTableRef(t, e, "a"), authorization.SelectPrivilege)
				right := enginetest.RoleTableRef(t, e, "b")
				grant(right, 0)
				key, value := right, right
				key.Address.SubID = 1
				value.Address.SubID = 2
				grant(key, authorization.SelectPrivilege)
				grant(value, authorization.SelectPrivilege)
				enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION join_reader")
				ast := enginetest.Statement(t, tc.query)
				var expected *engine.StatementDescription
				if prepared {
					description, err := e.Describe(op, ast)
					if err != nil {
						t.Fatal(err)
					}
					expected = &description
				}
				execute := func(denied bool) {
					t.Helper()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					scope.Expected = expected
					result, err := scope.Execute(ast, 0, nil)
					err = scope.Finish(err)
					if denied {
						var diagnostic *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table b"}
						if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 {
							t.Fatal(result, diagnostic, err)
						}
						return
					}
					if err != nil || result.Rows == nil {
						t.Fatal(result, err)
					}
					result.Rows.Close()
				}
				execute(false)
				switch tc.required {
				case 1:
					grant(key, 0)
				case 2:
					grant(value, 0)
				default:
					grant(key, 0)
					grant(value, 0)
				}
				// b is empty: authorization cannot be conditional on finding an
				// admitted pair. An unrelated granted column is not a key grant.
				execute(true)
			})
		}
	}
}

func TestQueryJoinsWideAliasAndSelfSourceACL(t *testing.T) {
	var columns, aliases []string
	for i := 1; i <= 70; i++ {
		columns = append(columns, "c"+strconv.Itoa(i)+" INT")
		aliases = append(aliases, "r"+strconv.Itoa(i))
	}
	aliases[69] = "last"
	for _, self := range []bool{false, true} {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "CREATE USER wide_join_reader")
		enginetest.RoleExecute(t, e, op, "CREATE TABLE a(k INT)")
		enginetest.RoleExecute(t, e, op, "CREATE TABLE b("+strings.Join(columns, ",")+")")
		reader, _ := e.Roles().Lookup("wide_join_reader")
		grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
			t.Helper()
			if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
				t.Fatal(err)
			}
		}
		grant(enginetest.RoleTableRef(t, e, "a"), authorization.SelectPrivilege)
		right := enginetest.RoleTableRef(t, e, "b")
		grant(right, 0)
		first, last := right, right
		first.Address.SubID = 1
		last.Address.SubID = 70
		grant(first, authorization.SelectPrivilege)
		grant(last, authorization.SelectPrivilege)
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION wide_join_reader")
		q := "SELECT x.last FROM a CROSS JOIN b x(" + strings.Join(aliases, ",") + ")"
		if self {
			q = "SELECT x.last,y.c1 FROM b x(" + strings.Join(aliases, ",") + ") CROSS JOIN b y"
		}
		ast := enginetest.Statement(t, q)
		description, err := e.Describe(op, ast)
		if err != nil || len(description.Results) == 0 || description.Results[0].OriginAttribute != 70 {
			t.Fatal(description, err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		result, err := scope.Execute(ast, 0, nil)
		err = scope.Finish(err)
		if err != nil || result.Count != 0 || result.Rows == nil {
			t.Fatal(result, err)
		}
		result.Rows.Close()
		grant(last, 0)
		scope, err = e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		result, err = scope.Execute(ast, 0, nil)
		err = scope.Finish(err)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table b"}
		if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 {
			t.Fatal(result, diagnostic, err)
		}
	}
}

func TestQueryJoinsInsertDestinationPriority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER insert_join_reader", "CREATE TABLE dst(k INT)", "CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	_, err := e.Describe(op, enginetest.Statement(t, "INSERT INTO dst(missing) SELECT a.k FROM a CROSS JOIN absent"))
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" of relation "dst" does not exist`, Position: "17"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(diagnostic, err)
	}
	reader, _ := e.Roles().Lookup("insert_join_reader")
	grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, name := range []string{"dst", "a", "b"} {
		if err = enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION insert_join_reader")
	ast := enginetest.Statement(t, "INSERT INTO dst SELECT 1 FROM a CROSS JOIN b")
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &description
	result, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	want = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table dst"}
	if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 {
		t.Fatal(result, diagnostic, err)
	}
}
