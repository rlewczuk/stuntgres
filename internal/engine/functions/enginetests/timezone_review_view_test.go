package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

func TestTimezoneReviewViewAdmittedEnvironment(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"SET TIME ZONE 'UTC'",
		"SET DateStyle='ISO, MDY'",
		"CREATE TABLE tz_view_source(id INT)",
		"INSERT INTO tz_view_source VALUES(1)",
		"CREATE VIEW tz_view AS SELECT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS same,DATE '2003-01-02'::TEXT AS rendered FROM tz_view_source",
		"CREATE VIEW tz_nested WITH(security_invoker=true) AS SELECT same,rendered FROM tz_view",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	ref := enginetest.RoleTableRef(t, e, "tz_view")
	definition := stored.ViewOf(e.Catalog.State.MustRelation(ref.Address))
	for _, name := range []string{"tz_view", "tz_nested"} {
		ast := enginetest.Statement(t, "SELECT same,rendered FROM "+name)
		description, err := e.Describe(op, ast)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			zone, style, text string
			want              bool
		}{
			{"UTC", "ISO, MDY", "2003-01-02", false},
			{"America/New_York", "SQL, DMY", "02/01/2003", true},
			{"UTC", "ISO, MDY", "2003-01-02", false},
		} {
			enginetest.RoleExecute(t, e, op, "SET TIME ZONE '"+tc.zone+"'")
			enginetest.RoleExecute(t, e, op, "SET DateStyle='"+tc.style+"'")
			for _, prepared := range []bool{false, true} {
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if prepared {
					scope.Expected = &description
				}
				result, err := scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				if err != nil {
					t.Fatal(err)
				}
				enginetest.RoleExecute(t, e, op, "SET TIME ZONE 'UTC'")
				enginetest.RoleExecute(t, e, op, "SET DateStyle='German, DMY'")
				rows := enginetest.Tuples(result)
				result.Rows.Close()
				if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != tc.want || rows[0][1] != tc.text {
					t.Fatalf("%s prepared=%t zone=%s: %v", name, prepared, tc.zone, rows)
				}
				enginetest.RoleExecute(t, e, op, "SET TIME ZONE '"+tc.zone+"'")
				enginetest.RoleExecute(t, e, op, "SET DateStyle='"+tc.style+"'")
			}
		}
	}
	if stored.ViewOf(e.Catalog.State.MustRelation(ref.Address)) != definition {
		t.Fatal("execution replaced the immutable view definition")
	}
}
