package tables_test

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestCommentInquiryAddressesAndAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER reader", "CREATE SCHEMA private", "CREATE TABLE private.t(a INT,b TEXT,c BIGINT)", "COMMENT ON TABLE private.t IS 'whole'", "COMMENT ON COLUMN private.t.a IS 'first'", "COMMENT ON COLUMN private.t.c IS 'third'", "COMMENT ON SCHEMA private IS 'schema'", "COMMENT ON ROLE reader IS 'shared'"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	oid := enginetest.RoleTableRef(t, e, "private.t").Address.ObjectID
	ns := enginetest.SchemaRef(t, e, "private").Address.ObjectID
	role, _ := e.Roles().Lookup("reader")
	reader, closeReader := enginetest.RoleOperation(t, e, "reader")
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
		r := enginetest.RoleExecute(t, e, reader, "SELECT "+tc.expr)
		if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
			t.Fatal(tc.expr, got, tc.want)
		}
		if s := r.Rows.Schema(); len(s) != 1 || s[0].Usage.Type.ID() != sqltype.Text {
			t.Fatal(s)
		}
	}
	enginetest.RoleExecute(t, e, op, "ALTER TABLE private.t DROP b")
	for _, tc := range []struct {
		n    int
		want driver.Value
	}{{-1, nil}, {0, "whole"}, {1, "first"}, {2, nil}, {3, "third"}, {99, nil}} {
		r := enginetest.RoleExecute(t, e, reader, fmt.Sprintf("SELECT col_description(%d,%d)", oid, tc.n))
		if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{tc.want}}) {
			t.Fatal(tc.n, got)
		}
	}
}
