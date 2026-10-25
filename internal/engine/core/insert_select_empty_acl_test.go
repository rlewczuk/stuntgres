package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestInsertSelectEmptyManifestACL(t *testing.T) {
	for _, tc := range []struct {
		name, query, denied                                  string
		emptySource                                          bool
		sourceTable, sourceColumn, targetTable, targetColumn authorization.Privileges
		count                                                int64
		values                                               []driver.Value
	}{
		{name: "constant_column_select", query: "INSERT INTO t SELECT 7 FROM s", sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, count: 3, values: []driver.Value{int64(7), int64(7), int64(7), int64(9)}},
		{name: "constant_empty_relation", query: "INSERT INTO t SELECT 7 FROM s", emptySource: true, sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, values: []driver.Value{int64(9)}},
		{name: "constant_filtered", query: "INSERT INTO t SELECT 7 FROM s WHERE FALSE", sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, values: []driver.Value{int64(9)}},
		{name: "zero_width_column_select", query: "INSERT INTO t SELECT FROM s", sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, count: 3, values: []driver.Value{int64(9), nil, nil, nil}},
		{name: "zero_width_empty_relation", query: "INSERT INTO t SELECT FROM s", emptySource: true, sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, values: []driver.Value{int64(9)}},
		{name: "zero_width_filtered", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceColumn: authorization.SelectPrivilege, targetTable: authorization.InsertPrivilege, values: []driver.Value{int64(9)}},
		{name: "empty_target_singleton", query: "INSERT INTO t (SELECT)", targetColumn: authorization.InsertPrivilege, count: 1, values: []driver.Value{int64(9), nil}},
		{name: "empty_target_source", query: "INSERT INTO t SELECT FROM s", sourceTable: authorization.SelectPrivilege, targetColumn: authorization.InsertPrivilege, count: 3, values: []driver.Value{int64(9), nil, nil, nil}},
		{name: "both_empty_column_manifests", query: "INSERT INTO t SELECT FROM s", sourceColumn: authorization.SelectPrivilege, targetColumn: authorization.InsertPrivilege, count: 3, values: []driver.Value{int64(9), nil, nil, nil}},
		{name: "empty_target_filtered", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceColumn: authorization.SelectPrivilege, targetColumn: authorization.InsertPrivilege, values: []driver.Value{int64(9)}},
		{name: "constant_no_select", query: "INSERT INTO t SELECT 7 FROM s", targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "empty_relation_no_select", query: "INSERT INTO t SELECT 7 FROM s", emptySource: true, targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "filtered_no_select", query: "INSERT INTO t SELECT 7 FROM s WHERE FALSE", targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "zero_width_no_select", query: "INSERT INTO t SELECT FROM s", targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "zero_width_filtered_no_select", query: "INSERT INTO t SELECT FROM s WHERE FALSE", targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "source_wrong_column_privilege", query: "INSERT INTO t SELECT 7 FROM s", sourceColumn: authorization.InsertPrivilege, targetTable: authorization.InsertPrivilege, denied: "s", values: []driver.Value{int64(9)}},
		{name: "empty_target_no_insert", query: "INSERT INTO t (SELECT)", denied: "t", values: []driver.Value{int64(9)}},
		{name: "empty_target_filtered_no_insert", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceColumn: authorization.SelectPrivilege, denied: "t", values: []driver.Value{int64(9)}},
		{name: "target_wrong_column_privilege", query: "INSERT INTO t (SELECT)", targetColumn: authorization.SelectPrivilege, denied: "t", values: []driver.Value{int64(9)}},
	} {
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepared"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				enginetest.RoleExecute(t, e, op, "CREATE USER worker")
				enginetest.RoleExecute(t, e, op, "CREATE TABLE s(unused INT,i INT)")
				enginetest.RoleExecute(t, e, op, "CREATE TABLE t(unused INT,i INT)")
				// The surviving live column has stable SubID 2, not dense slot+1.
				enginetest.RoleExecute(t, e, op, "ALTER TABLE s DROP unused")
				enginetest.RoleExecute(t, e, op, "ALTER TABLE t DROP unused")
				if !tc.emptySource {
					enginetest.RoleExecute(t, e, op, "INSERT INTO s VALUES(1),(1),(2)")
				}
				enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(9)")
				worker, _ := e.Roles().Lookup("worker")
				grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				grant := func(ref ids.ObjectRef, mask authorization.Privileges) {
					t.Helper()
					if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: worker.Ref}, Privileges: mask}}}); err != nil {
						t.Fatal(err)
					}
				}
				source, target := enginetest.RoleTableRef(t, e, "s"), enginetest.RoleTableRef(t, e, "t")
				grant(source, tc.sourceTable)
				grant(target, tc.targetTable)
				source.Address.SubID = 2
				target.Address.SubID = 2
				grant(source, tc.sourceColumn)
				grant(target, tc.targetColumn)
				enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION worker")
				ast := enginetest.Statement(t, tc.query)
				var expected *core.StatementDescription
				if prepared {
					desc, err := e.Describe(op, ast)
					if err != nil || len(desc.Results) != 0 || desc.Parameters.Count != 0 {
						t.Fatal(desc, err)
					}
					expected = &desc
				}
				t.Run("consumer", func(t *testing.T) {
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					scope.Expected = expected
					r, err := scope.Execute(ast, 0, nil)
					err = scope.Finish(err)
					if tc.denied != "" {
						var got *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table " + tc.denied}
						if !errors.As(err, &got) || *got != want || r.Count != 0 || r.Rows != nil {
							t.Fatalf("result %#v, error %#v (%v), want %#v", r, got, err, want)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if r.Count != tc.count || r.Rows != nil {
						t.Fatalf("command result %#v, want count %d and no rows", r, tc.count)
					}
				})
				enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
				want := make([][]driver.Value, len(tc.values))
				for i, value := range tc.values {
					want[i] = []driver.Value{value}
				}
				if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT i FROM t ORDER BY i")); !reflect.DeepEqual(got, want) {
					t.Fatalf("destination got %#v, want %#v", got, want)
				}
			})
		}
	}
	for _, table := range []string{"s", "t"} {
		t.Run("dropped_grant_"+table, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE USER worker")
			enginetest.RoleExecute(t, e, op, "CREATE TABLE s(i INT,j INT)")
			enginetest.RoleExecute(t, e, op, "CREATE TABLE t(i INT,j INT)")
			enginetest.RoleExecute(t, e, op, "INSERT INTO s VALUES(1,2),(1,2)")
			enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(9,9)")
			worker, _ := e.Roles().Lookup("worker")
			grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			grant := func(ref ids.ObjectRef, mask authorization.Privileges) {
				t.Helper()
				if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: worker.Ref}, Privileges: mask}}}); err != nil {
					t.Fatal(err)
				}
			}
			q := "INSERT INTO t SELECT 7 FROM s"
			want := [][]driver.Value{{int64(7)}, {int64(7)}, {int64(9)}}
			ref := enginetest.RoleTableRef(t, e, table)
			ref.Address.SubID = 2
			if table == "s" {
				grant(ref, authorization.SelectPrivilege)
				grant(enginetest.RoleTableRef(t, e, "t"), authorization.InsertPrivilege)
			} else {
				grant(ref, authorization.InsertPrivilege)
				grant(enginetest.RoleTableRef(t, e, "s"), authorization.SelectPrivilege)
				q = "INSERT INTO t SELECT FROM s"
				want = [][]driver.Value{{int64(9)}, {nil}, {nil}}
			}
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION worker")
			if r := enginetest.RoleExecute(t, e, op, q); r.Count != 2 || r.Rows != nil {
				t.Fatal(r)
			}
			enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			enginetest.RoleExecute(t, e, op, "ALTER TABLE "+table+" DROP j")
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION worker")
			enginetest.RoleError(t, e, op, q, "42501", "permission denied for table "+table, "")
			enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT i FROM t ORDER BY i")); !reflect.DeepEqual(got, want) {
				t.Fatalf("destination got %#v, want %#v", got, want)
			}
		})
	}
}
