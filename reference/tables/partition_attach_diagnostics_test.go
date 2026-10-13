package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete diagnostics and bound effects were independently predicted, then
// checked against pinned PostgreSQL 18.0 before native implementation.
func TestReferencePartitionAttachDiagnostics(t *testing.T) {
	cases := []struct {
		name  string
		setup []string
		query string
		want  fixture.Diagnostic
		token string
		ok    bool
	}{
		{name: "ordinary_parent", setup: []string{"CREATE TABLE pa_parent(k INT)"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action ATTACH PARTITION cannot be performed on relation "pa_parent"`, Detail: "This operation is not supported for tables."}},
		{name: "missing_parent", query: "ALTER TABLE pa_absent ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3)", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pa_absent" does not exist`}},
		{name: "missing_candidate", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_absent FOR VALUES FROM(1) TO(3)", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pa_absent" does not exist`}},
		{name: "only", query: "ALTER TABLE ONLY pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3)", ok: true},
		{name: "star", query: "ALTER TABLE pa_parent * ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3)", ok: true},
		{name: "action_list", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3), ADD COLUMN j INT", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`}, token: ","},
		{name: "trailing_suffix", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3) nonsense", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`}, token: "nonsense"},
		{name: "extra_column", setup: []string{"CREATE TABLE pa_child(k INT,j INT)"}, want: fixture.Diagnostic{Code: "42804", Message: `table "pa_child" contains column "j" not found in parent "pa_parent"`, Detail: "The new partition may contain only the columns present in parent."}},
		{name: "wrong_name_same_count", setup: []string{"CREATE TABLE pa_child(j INT)"}, want: fixture.Diagnostic{Code: "42804", Message: `table "pa_child" contains column "j" not found in parent "pa_parent"`, Detail: "The new partition may contain only the columns present in parent."}},
		{name: "missing_column", setup: []string{"CREATE TABLE pa_parent(k INT,j INT) PARTITION BY RANGE(k)"}, want: fixture.Diagnostic{Code: "42804", Message: `child table is missing column "j"`}},
		{name: "type", setup: []string{"CREATE TABLE pa_child(k BIGINT)"}, want: fixture.Diagnostic{Code: "42804", Message: `child table "pa_child" has different type for column "k"`}},
		{name: "typmod", setup: []string{"CREATE TABLE pa_parent(k VARCHAR(3)) PARTITION BY RANGE(k)", "CREATE TABLE pa_child(k VARCHAR(4))"}, query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM('a') TO('z')", want: fixture.Diagnostic{Code: "42804", Message: `child table "pa_child" has different type for column "k"`}},
		{name: "collation", setup: []string{`CREATE TABLE pa_parent(k TEXT COLLATE "C") PARTITION BY RANGE(k)`, `CREATE TABLE pa_child(k TEXT COLLATE "POSIX")`}, query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM('a') TO('z')", want: fixture.Diagnostic{Code: "42P21", Message: `child table "pa_child" has different collation for column "k"`}},
		{name: "missing_not_null", setup: []string{"CREATE TABLE pa_parent(k INT NOT NULL) PARTITION BY RANGE(k)"}, want: fixture.Diagnostic{Code: "42804", Message: `column "k" in child table "pa_child" must be marked NOT NULL`}},
		{name: "missing_check", setup: []string{"CREATE TABLE pa_parent(k INT CONSTRAINT pa_check CHECK(k>0)) PARTITION BY RANGE(k)"}, want: fixture.Diagnostic{Code: "42804", Message: `child table is missing constraint "pa_check"`}},
		{name: "different_check_name", setup: []string{"CREATE TABLE pa_parent(k INT CONSTRAINT pa_check CHECK(k>0)) PARTITION BY RANGE(k)", "CREATE TABLE pa_child(k INT CONSTRAINT other_check CHECK(k>1))"}, want: fixture.Diagnostic{Code: "42804", Message: `child table is missing constraint "pa_check"`}},
		{name: "different_check_definition", setup: []string{"CREATE TABLE pa_parent(k INT CONSTRAINT pa_check CHECK(k>0)) PARTITION BY RANGE(k)", "CREATE TABLE pa_child(k INT CONSTRAINT pa_check CHECK(k>=0))"}, want: fixture.Diagnostic{Code: "42804", Message: `child table "pa_child" has different definition for check constraint "pa_check"`}},
		{name: "no_inherit_check", setup: []string{"CREATE TABLE pa_parent(k INT CONSTRAINT pa_check CHECK(k>0)) PARTITION BY RANGE(k)", "CREATE TABLE pa_child(k INT CONSTRAINT pa_check CHECK(k>0) NO INHERIT)"}, want: fixture.Diagnostic{Code: "42P17", Message: `constraint "pa_check" conflicts with non-inherited constraint on child table "pa_child"`}},
		{name: "not_valid_check", setup: []string{"CREATE TABLE pa_parent(k INT CONSTRAINT pa_check CHECK(k>0)) PARTITION BY RANGE(k)", "CREATE TABLE pa_child(k INT)", "ALTER TABLE pa_child ADD CONSTRAINT pa_check CHECK(k>0) NOT VALID"}, want: fixture.Diagnostic{Code: "42P17", Message: `constraint "pa_check" conflicts with NOT VALID constraint on child table "pa_child"`}},
		{name: "already_partition", setup: []string{"CREATE TABLE pa_other(k INT) PARTITION BY RANGE(k)", "CREATE TABLE pa_child PARTITION OF pa_other DEFAULT"}, want: fixture.Diagnostic{Code: "42809", Message: `"pa_child" is already a partition`}},
		{name: "typed_candidate", setup: []string{"CREATE TYPE pa_pair AS(k INT)", "CREATE TABLE pa_child OF pa_pair"}, want: fixture.Diagnostic{Code: "42809", Message: "cannot attach a typed table as partition"}},
		{name: "inheritance_child", setup: []string{"CREATE TABLE pa_other(k INT)", "CREATE TABLE pa_child() INHERITS(pa_other)"}, want: fixture.Diagnostic{Code: "42809", Message: "cannot attach inheritance child as partition"}},
		{name: "inheritance_parent", setup: []string{"CREATE TABLE pa_child(k INT)", "CREATE TABLE pa_other() INHERITS(pa_child)"}, want: fixture.Diagnostic{Code: "42809", Message: "cannot attach inheritance parent as partition"}},
		{name: "self_cycle", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_parent DEFAULT", want: fixture.Diagnostic{Code: "42P07", Message: "circular inheritance not allowed", Detail: `"pa_parent" is already a child of "pa_parent".`}},
		{name: "ancestor_cycle", setup: []string{"CREATE TABLE pa_child(k INT) PARTITION BY RANGE(k)", "CREATE TABLE pa_parent PARTITION OF pa_child DEFAULT PARTITION BY RANGE(k)"}, query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child DEFAULT", want: fixture.Diagnostic{Code: "42P07", Message: "circular inheritance not allowed", Detail: `"pa_parent" is already a child of "pa_child".`}},
		{name: "identity", setup: []string{"CREATE TABLE pa_child(k INT GENERATED BY DEFAULT AS IDENTITY)"}, want: fixture.Diagnostic{Code: "55000", Message: `table "pa_child" being attached contains an identity column "k"`, Detail: "The new partition may not contain an identity column."}},
		{name: "candidate_view", setup: []string{"CREATE VIEW pa_child AS SELECT 1 AS k"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action ATTACH PARTITION cannot be performed on relation "pa_child"`, Detail: "This operation is not supported for views."}},
		{name: "second_default", setup: []string{"CREATE TABLE pa_default PARTITION OF pa_parent DEFAULT"}, query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child DEFAULT", want: fixture.Diagnostic{Code: "42P17", Message: `partition "pa_child" conflicts with existing default partition "pa_default"`}, token: "DEFAULT"},
		{name: "overlap", setup: []string{"CREATE TABLE pa_old PARTITION OF pa_parent FOR VALUES FROM(1) TO(3)"}, want: fixture.Diagnostic{Code: "42P17", Message: `partition "pa_child" would overlap partition "pa_old"`}, token: "1) TO"},
		{name: "empty", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(3) TO(3)", want: fixture.Diagnostic{Code: "42P17", Message: `empty range bound specified for partition "pa_child"`, Detail: "Specified lower bound (3) is greater than or equal to upper bound (3)."}, token: "3) TO"},
		{name: "null_bound", query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(NULL) TO(3)", want: fixture.Diagnostic{Code: "42P17", Message: "cannot specify NULL in range bound"}},
		{name: "candidate_row", setup: []string{"CREATE TABLE pa_child(k INT)", "INSERT INTO pa_child VALUES(1),(3)"}, want: fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pa_child" is violated by some row`, Schema: "public", Table: "pa_child"}},
		{name: "candidate_null", setup: []string{"CREATE TABLE pa_child(k INT)", "INSERT INTO pa_child VALUES(NULL)"}, want: fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pa_child" is violated by some row`, Schema: "public", Table: "pa_child"}},
		{name: "default_row", setup: []string{"CREATE TABLE pa_old PARTITION OF pa_parent FOR VALUES FROM(1) TO(3)", "INSERT INTO pa_child VALUES(2)"}, query: "ALTER TABLE pa_parent ATTACH PARTITION pa_child DEFAULT", want: fixture.Diagnostic{Code: "23514", Message: `partition constraint of relation "pa_child" is violated by some row`, Schema: "public", Table: "pa_child"}},
		{name: "incumbent_default_row", setup: []string{"CREATE TABLE pa_default PARTITION OF pa_parent DEFAULT", "INSERT INTO pa_default VALUES(2)"}, want: fixture.Diagnostic{Code: "23514", Message: `updated partition constraint for default partition "pa_default" would be violated by some row`, Schema: "public", Table: "pa_default"}},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(c.name+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						hasParent, hasChild := false, false
						for _, q := range c.setup {
							if strings.HasPrefix(q, "CREATE TABLE pa_parent") {
								hasParent = true
							}
							if strings.HasPrefix(q, "CREATE TABLE pa_child") || strings.HasPrefix(q, "CREATE VIEW pa_child") {
								hasChild = true
							}
						}
						if !hasParent {
							run(fixture.Exec("CREATE TABLE pa_parent(k INT) PARTITION BY RANGE(k)", 0))
						}
						if !hasChild {
							run(fixture.Exec("CREATE TABLE pa_child(k INT)", 0))
						}
						for _, q := range c.setup {
							// Setup DML may affect rows; its count is not the attach contract.
							if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
								t.Fatal(q, err)
							}
						}
						q := c.query
						if q == "" {
							q = "ALTER TABLE pa_parent ATTACH PARTITION pa_child FOR VALUES FROM(1) TO(3)"
						}
						want := c.want
						if c.token != "" {
							want.Position = referenceRangePosition(q, c.token)
						}
						step := fixture.ImplicitError(q, want)
						if c.ok {
							step = fixture.Exec(q, 0)
						}
						step.Prepared = prepared
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) })
						// A rejection must not add an edge, even if a bound or schema
						// diagnostic failed its independently predicted optional fields.
						if !c.ok {
							run(fixture.Select("SELECT COUNT(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid JOIN pg_class p ON p.oid=d.refobjid WHERE c.relname='pa_child' AND p.relname='pa_parent' AND d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					})
				}
			})
		}
	}
	t.Run("if_exists_notice_and_skip", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
				run(fixture.Exec("CREATE SEQUENCE pa_skip_seq START 10", 0))
				q := "ALTER TABLE IF EXISTS pa_absent ATTACH PARTITION also_absent FOR VALUES FROM(nextval('pa_skip_seq')) TO(20)"
				for _, prepared := range []bool{false, true} {
					notices = nil
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					run(s)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "pa_absent" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Errorf("notices got %#v want %#v", notices, want)
					}
					run(fixture.Select("SELECT last_value,is_called FROM pa_skip_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), false}}))
				}
				bad := q + ", ADD COLUMN j INT"
				run(fixture.ImplicitError(bad, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: referenceRangePosition(bad, ",")}))
			})
		}
	})
	partitionAttachBoundPrecedence(t)
	partitionAttachOwnerDiagnostics(t)
	partitionAttachCheckAlterBoundary(t)
	partitionAttachGeneratedDiagnostics(t)
	partitionAttachEligibilityDiagnostics(t)
}
