package engine

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCommentInquiryAddressesAndAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER reader", "CREATE SCHEMA private", "CREATE TABLE private.t(a INT,b TEXT,c BIGINT)", "COMMENT ON TABLE private.t IS 'whole'", "COMMENT ON COLUMN private.t.a IS 'first'", "COMMENT ON COLUMN private.t.c IS 'third'", "COMMENT ON SCHEMA private IS 'schema'", "COMMENT ON ROLE reader IS 'shared'"} {
		roleExecute(t, e, op, q)
	}
	oid := roleTableRef(t, e, "private.t").Address.ObjectID
	ns := schemaRef(t, e, "private").Address.ObjectID
	role, _ := e.Roles().Lookup("reader")
	reader, closeReader := roleOperation(t, e, "reader")
	defer closeReader()
	for _, tc := range []struct {
		expr string
		want driver.Value
	}{
		{fmt.Sprintf("obj_description(%d,'pg_class')", oid), "whole"},
		{fmt.Sprintf("obj_description(%d)", oid), "whole"},
		{fmt.Sprintf("col_description(%d,0)", oid), "whole"},
		{fmt.Sprintf("col_description(%d,1)", oid), "first"},
		{fmt.Sprintf("col_description(%d,3)", oid), "third"},
		{fmt.Sprintf("obj_description(%d,'pg_namespace')", ns), "schema"},
		{fmt.Sprintf("shobj_description(%d,'pg_authid')", role.Ref.ID()), "shared"},
		{fmt.Sprintf("obj_description(%d,'pg_authid')", role.Ref.ID()), nil},
		{fmt.Sprintf("shobj_description(%d,'pg_class')", oid), nil},
		{"col_description(NULL,1)", nil}, {"obj_description(NULL)", nil},
		{"obj_description(0,'pg_class')", nil}, {"shobj_description(NULL,'pg_authid')", nil},
	} {
		r := roleExecute(t, e, reader, "SELECT "+tc.expr)
		if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
			t.Fatal(tc.expr, got, tc.want)
		}
		if s := r.Rows.Schema(); len(s) != 1 || s[0].Usage.Type.ID() != sqltype.Text {
			t.Fatal(s)
		}
	}
	roleExecute(t, e, op, "ALTER TABLE private.t DROP b")
	for _, tc := range []struct {
		n    int
		want driver.Value
	}{{-1, nil}, {0, "whole"}, {1, "first"}, {2, nil}, {3, "third"}, {99, nil}} {
		r := roleExecute(t, e, reader, fmt.Sprintf("SELECT col_description(%d,%d)", oid, tc.n))
		if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
			t.Fatal(tc.n, got)
		}
	}
}

func TestCommentInquiryParameterMetadata(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, tc := range []struct {
		q   string
		ids []sqltype.ID
	}{
		{"SELECT col_description($1,$2)", []sqltype.ID{sqltype.Oid, sqltype.Int4}},
		{"SELECT obj_description($1,$2)", []sqltype.ID{sqltype.Oid, sqltype.Name}},
		{"SELECT obj_description($1)", []sqltype.ID{sqltype.Oid}},
		{"SELECT shobj_description($1,$2)", []sqltype.ID{sqltype.Oid, sqltype.Name}},
	} {
		d, err := e.Describe(op, statement(t, tc.q))
		if err != nil {
			t.Fatal(tc.q, err)
		}
		if d.Parameters.Count != len(tc.ids) || len(d.Parameters.Types) != len(tc.ids) || len(d.Results) != 1 || d.Results[0].Usage.Type.ID() != sqltype.Text {
			t.Fatal(d)
		}
		for i, id := range tc.ids {
			if d.Parameters.Types[i].Type.ID() != id {
				t.Fatal(tc.q, i, d.Parameters.Types)
			}
		}
	}
}

func TestCommentInquiryRenames(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE s.t(a INT)", "COMMENT ON SCHEMA s IS 'schema'", "COMMENT ON TABLE s.t IS 'table'", "COMMENT ON COLUMN s.t.a IS 'column'"} {
		roleExecute(t, e, op, q)
	}
	oid := roleTableRef(t, e, "s.t").Address.ObjectID
	ns := schemaRef(t, e, "s").Address.ObjectID
	for _, q := range []string{"ALTER TABLE s.t RENAME COLUMN a TO renamed", "ALTER TABLE s.t RENAME TO renamed", "ALTER SCHEMA s RENAME TO renamed"} {
		roleExecute(t, e, op, q)
	}
	for _, tc := range []struct{ q, want string }{
		{fmt.Sprintf("SELECT obj_description(%d,'pg_namespace')", ns), "schema"},
		{fmt.Sprintf("SELECT obj_description(%d,'pg_class')", oid), "table"},
		{fmt.Sprintf("SELECT col_description(%d,1)", oid), "column"},
	} {
		got := tuples(roleExecute(t, e, op, tc.q))
		if len(got) != 1 || got[0][0] != tc.want {
			t.Fatal(tc.q, got)
		}
	}
}
