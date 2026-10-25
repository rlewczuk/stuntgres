package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionCheckMerge(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			parent := "rp_merge_parent_" + route.name
			leaf := "rp_merge_leaf_" + route.name
			constraint := "rp_merge_check"
			run(fixture.Exec("CREATE TABLE "+parent+"(k INT,CONSTRAINT "+constraint+" CHECK(k>0)) PARTITION BY RANGE(k)", 0))
			notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `merging constraint "rp_merge_check" with inherited definition`}
			notices = nil
			create := fixture.Exec("CREATE TABLE "+leaf+" PARTITION OF "+parent+"(CONSTRAINT "+constraint+" CHECK(k>0)) FOR VALUES FROM(0) TO(10)", 0)
			create.Prepared = route.prepared
			run(create)
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{notice}) {
				t.Fatalf("%s equal merge notices got %#v", route.name, notices)
			}
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='"+leaf+"'::regclass::OID AND contype='c'", []string{"conname"}, []string{"NAME"}, [][]any{{constraint}}))
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+parent+" VALUES($1)", 1, 1)))
			failure := referenceRangeError("INSERT INTO "+parent+" VALUES(2),(0)", "23514", `new row for relation "`+leaf+`" violates check constraint "rp_merge_check"`, fixture.Diagnostic{Detail: "Failing row contains (0).", Schema: "public", Table: leaf, Constraint: constraint})
			failure.Prepared = route.prepared
			run(failure)
			run(referenceRangeRows("SELECT k FROM "+parent+" ORDER BY k", 1))
			for _, tc := range []struct {
				name, definition, code, message string
				mergedNotice                    bool
			}{
				{"commuted", "CHECK(0<k)", "42710", "", false},
				{"arithmetic", "CHECK(k>0+0)", "42710", "", false},
				{"different", "CHECK(k>=0)", "42710", "", false},
				{"equal_noinherit", "CHECK(k>0) NO INHERIT", "42P17", "", false},
				{"different_noinherit", "CHECK(k>=0) NO INHERIT", "42710", "", false},
				{"twice", "CHECK(k>0),CONSTRAINT rp_merge_check CHECK(k>0)", "42710", `check constraint "rp_merge_check" already exists`, true},
			} {
				child := "rp_merge_" + tc.name + "_" + route.name
				message := tc.message
				if message == "" {
					message = `constraint "rp_merge_check" for relation "` + child + `" already exists`
				}
				if tc.code == "42P17" {
					message = `constraint "rp_merge_check" conflicts with inherited constraint on relation "` + child + `"`
				}
				notices = nil
				step := referenceRangeError("CREATE TABLE "+child+" PARTITION OF "+parent+"(CONSTRAINT rp_merge_check "+tc.definition+") FOR VALUES FROM(10) TO(20)", tc.code, message, fixture.Diagnostic{})
				step.Prepared = route.prepared
				run(step)
				var expected []fixture.Diagnostic
				if tc.mergedNotice {
					expected = []fixture.Diagnostic{notice}
				}
				if !reflect.DeepEqual(notices, expected) {
					t.Fatalf("%s/%s notices got %#v want %#v", route.name, tc.name, notices, expected)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='"+child+"'", []string{"relname"}, []string{"NAME"}, nil))
				run(referenceRangeRows("SELECT k FROM "+parent+" ORDER BY k", 1))
			}
		}
	})
}
