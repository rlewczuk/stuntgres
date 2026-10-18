package engine

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestViewReplaceCurrentVisibilityCaptureDemand(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE ids(id OID)", "INSERT INTO ids VALUES(23),(NULL)", "CREATE VIEW v AS SELECT FALSE AS visible FROM ids", "CREATE VIEW w AS SELECT visible FROM v", "CREATE VIEW z AS SELECT visible FROM w", "CREATE TABLE destination(visible BOOL)"} {
		sessionSQL(t, s, q)
	}
	read := func(query string, captured bool, want [][]driver.Value) {
		t.Helper()
		stmt := statement(t, query)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { scope.Finish(err) }()
		result, err := scope.Execute(stmt, 0, nil)
		if result.Rows != nil {
			defer result.Rows.Close()
		}
		if err != nil {
			t.Fatal(query, err)
		}
		if got := scope.visibilityCatalog.State() != nil; got != captured {
			t.Fatalf("%s visibility capture=%v want %v", query, got, captured)
		}
		if !captured && (scope.visibilityRelationPath != nil || scope.visibilityOtherPath != nil) {
			t.Fatal("unneeded visibility paths captured", query)
		}
		if want != nil {
			if got := tuples(result); !reflect.DeepEqual(got, want) {
				t.Fatal(query, got, want)
			}
		}
	}
	plain := [][]driver.Value{{false}, {false}}
	inquiry := [][]driver.Value{{true}, {nil}}
	read("SELECT visible FROM z ORDER BY visible", false, plain)
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT pg_type_is_visible(id) AS visible FROM ids")
	read("SELECT visible FROM w ORDER BY visible", true, inquiry)
	read("SELECT visible FROM z ORDER BY visible", true, inquiry)
	read("INSERT INTO destination SELECT visible FROM z", true, nil)
	sessionSQL(t, s, "CREATE VIEW late_dependent AS SELECT visible FROM v")
	sessionSQL(t, s, "CREATE VIEW late_outer AS SELECT visible FROM late_dependent")
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT FALSE AS visible FROM ids")
	read("SELECT visible FROM z ORDER BY visible", false, plain)
	read("SELECT visible FROM late_outer ORDER BY visible", false, plain)
	read("INSERT INTO destination SELECT visible FROM late_outer", false, nil)
	read("SELECT id IS NULL FROM ids ORDER BY id", false, [][]driver.Value{{false}, {true}})
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT pg_type_is_visible(id) AS visible FROM ids")
	read("SELECT visible FROM late_outer ORDER BY visible", true, inquiry)
	read("SELECT visible FROM z ORDER BY visible", true, inquiry)
	// Current-demand discovery must remain bounded on the admitted recursive
	// graph, leaving the ordinary read-expansion diagnostic authoritative.
	sessionSQL(t, s, "CREATE OR REPLACE VIEW v AS SELECT visible FROM v")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	_, err = e.Execute(op, statement(t, "SELECT visible FROM z"), 0, nil)
	viewReplaceError(t, err, sqlstate.Error{Code: "42P17", Message: `infinite recursion detected in rules for relation "v"`})
	lockEmpty(t, e)
}

func TestDropViewIndexWrongKindAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"DROP VIEW ix", "DROP VIEW IF EXISTS ix", "DROP VIEW v,ix"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE INDEX ix ON t(id)", "CREATE VIEW v AS SELECT id FROM t"} {
					roleExecute(t, e, op, q)
				}
				before := e.CatalogRead().State()
				refs := []ids.ObjectRef{roleTableRef(t, e, "t"), roleTableRef(t, e, "ix"), roleTableRef(t, e, "v")}
				stmt := statement(t, query)
				if prepared {
					if _, err := e.Describe(op, stmt); err != nil {
						t.Fatal(err)
					}
					if e.CatalogRead().State() != before {
						t.Fatal("Prepare published")
					}
					lockEmpty(t, e)
				}
				_, err := e.Execute(op, stmt, 0, nil)
				viewReplaceError(t, err, sqlstate.Error{Code: "42809", Message: `"ix" is not a view`, Hint: "Use DROP INDEX to remove an index."})
				if e.CatalogRead().State() != before {
					t.Fatal("wrong-kind DROP partially published")
				}
				for _, ref := range refs {
					if e.CatalogRead().Get(ref.Address) != before.Get(ref.Address) {
						t.Fatal("descriptor or identity changed", ref.Address)
					}
				}
				lockEmpty(t, e)
				if got := tuples(run(t, e, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal(got)
				}
				if got := tuples(run(t, e, "SELECT id FROM v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal(got)
				}
			})
		}
	}
}
