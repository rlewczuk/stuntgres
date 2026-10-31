package core_test

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestCommentInquiryParameterMetadata(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
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
		d, err := e.Describe(op, enginetest.Statement(t, tc.q))
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
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE s.t(a INT)", "COMMENT ON SCHEMA s IS 'schema'", "COMMENT ON TABLE s.t IS 'table'", "COMMENT ON COLUMN s.t.a IS 'column'"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	oid := enginetest.RoleTableRef(t, e, "s.t").Address.ObjectID
	ns := schemaRef(t, e, "s").Address.ObjectID
	for _, q := range []string{"ALTER TABLE s.t RENAME COLUMN a TO renamed", "ALTER TABLE s.t RENAME TO renamed", "ALTER SCHEMA s RENAME TO renamed"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	for _, tc := range []struct{ q, want string }{
		{fmt.Sprintf("SELECT obj_description(%d,'pg_namespace')", ns), "schema"},
		{fmt.Sprintf("SELECT obj_description(%d,'pg_class')", oid), "table"},
		{fmt.Sprintf("SELECT col_description(%d,1)", oid), "column"},
	} {
		got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, tc.q))
		if len(got) != 1 || got[0][0] != tc.want {
			t.Fatal(tc.q, got)
		}
	}
}
