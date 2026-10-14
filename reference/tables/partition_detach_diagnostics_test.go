package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionDetachDiagnostics(t *testing.T) {
	cases := []struct {
		name, query string
		setup       []string
		want        fixture.Diagnostic
		token       string
		ok          bool
	}{
		{name: "missing_parent", query: "ALTER TABLE pd_absent DETACH PARTITION pd_child", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_absent" does not exist`}},
		{name: "missing_child", query: "ALTER TABLE pd_parent DETACH PARTITION pd_absent", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_absent" does not exist`}},
		{name: "ordinary_parent", query: "ALTER TABLE pd_regular DETACH PARTITION pd_child", setup: []string{"CREATE TABLE pd_regular(k INT)"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "pd_regular"`, Detail: "This operation is not supported for tables."}},
		{name: "standalone", query: "ALTER TABLE pd_parent DETACH PARTITION pd_regular", setup: []string{"CREATE TABLE pd_regular(k INT)"}, want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_regular" is not a partition of relation "pd_parent"`}},
		{name: "wrong_parent", query: "ALTER TABLE pd_other DETACH PARTITION pd_child", setup: []string{"CREATE TABLE pd_other(k INT,payload TEXT) PARTITION BY RANGE(k)"}, want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_child" is not a partition of relation "pd_other"`}},
		{name: "grandchild", query: "ALTER TABLE pd_parent DETACH PARTITION pd_grandchild", setup: []string{"CREATE TABLE pd_mid_diag PARTITION OF pd_parent FOR VALUES FROM(5) TO(7) PARTITION BY RANGE(k)", "CREATE TABLE pd_grandchild PARTITION OF pd_mid_diag DEFAULT"}, want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_grandchild" is not a partition of relation "pd_parent"`}},
		{name: "self", query: "ALTER TABLE pd_parent DETACH PARTITION pd_parent", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_parent" is not a partition of relation "pd_parent"`}},
		{name: "parent_view", query: "ALTER TABLE pd_view DETACH PARTITION pd_child", setup: []string{"CREATE VIEW pd_view AS SELECT k FROM pd_child"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "pd_view"`, Detail: "This operation is not supported for views."}},
		{name: "parent_sequence", query: "ALTER TABLE pd_seq DETACH PARTITION pd_child", want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "pd_seq"`, Detail: "This operation is not supported for sequences."}},
		{name: "parent_index", query: "ALTER TABLE pd_child_key DETACH PARTITION pd_child", want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "pd_child_key"`, Detail: "This operation is not supported for indexes."}},
		{name: "parent_partitioned_index", query: "ALTER TABLE pd_key DETACH PARTITION pd_child", want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "pd_key"`, Detail: "This operation is not supported for partitioned indexes."}},
		{name: "candidate_view", query: "ALTER TABLE pd_parent DETACH PARTITION pd_view", setup: []string{"CREATE VIEW pd_view AS SELECT k FROM pd_child"}, want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_view" is not a partition of relation "pd_parent"`}},
		{name: "candidate_sequence", query: "ALTER TABLE pd_parent DETACH PARTITION pd_seq", want: fixture.Diagnostic{Code: "42P01", Message: `relation "pd_seq" is not a partition of relation "pd_parent"`}},
		{name: "candidate_index", query: "ALTER TABLE pd_parent DETACH PARTITION pd_child_key", want: fixture.Diagnostic{Code: "42809", Message: `cannot open relation "pd_child_key"`, Detail: "This operation is not supported for indexes."}},
		{name: "candidate_partitioned_index", query: "ALTER TABLE pd_parent DETACH PARTITION pd_key", want: fixture.Diagnostic{Code: "42809", Message: `cannot open relation "pd_key"`, Detail: "This operation is not supported for partitioned indexes."}},
		{name: "candidate_composite", query: "ALTER TABLE pd_parent DETACH PARTITION pd_composite", setup: []string{"CREATE TYPE pd_composite AS(k INT)"}, want: fixture.Diagnostic{Code: "42809", Message: `cannot open relation "pd_composite"`, Detail: "This operation is not supported for composite types."}},
		{name: "missing_schema", query: "ALTER TABLE pd_missing_schema.parent DETACH PARTITION pd_child", want: fixture.Diagnostic{Code: "3F000", Message: `schema "pd_missing_schema" does not exist`}},
		{name: "only", query: "ALTER TABLE ONLY pd_parent DETACH PARTITION pd_child", ok: true},
		{name: "star", query: "ALTER TABLE pd_parent * DETACH PARTITION pd_child", ok: true},
		{name: "action_list", query: "ALTER TABLE pd_parent DETACH PARTITION pd_child, ADD COLUMN j INT", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`}, token: ","},
		{name: "suffix", query: "ALTER TABLE pd_parent DETACH PARTITION pd_child nonsense", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`}, token: "nonsense"},
	}
	for _, c := range cases {
		for _, prepared := range []bool{false, true} {
			name := c.name + "/direct"
			if prepared {
				name = c.name + "/prepared"
			}
			t.Run(name, func(t *testing.T) {
				partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					partitionDetachSetup(run, "pd_parent")
					for _, q := range c.setup {
						run(fixture.Exec(q, 0))
					}
					graphQ := "SELECT classid::BIGINT,objid::BIGINT,objsubid,refclassid::BIGINT,refobjid::BIGINT,refobjsubid,deptype FROM pg_depend ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
					before := partitionDetachCapture(t, p, conn, graphQ)
					want := c.want
					if c.token != "" {
						want.Position = referenceRangePosition(c.query, c.token)
					}
					s := fixture.ImplicitError(c.query, want)
					if c.ok {
						s = fixture.Exec(c.query, 0)
					}
					s.Prepared = prepared
					if !c.ok {
						s.ErrorStage = "execute"
						if prepared && c.token != "" {
							s.ErrorStage = "prepare"
						}
					}
					t.Run("diagnostic", func(t *testing.T) { run(s) })
					if c.ok {
						run(referenceRangeRows("SELECT k FROM pd_parent", 3))
						run(partitionDetachClass("pd_child", false))
						return
					}
					if got := partitionDetachCapture(t, p, conn, graphQ); !reflect.DeepEqual(got, before) {
						t.Fatalf("rejected statement changed dependency graph: %#v", got)
					}
					run(referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 1, 2, 3))
					run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2))
					run(partitionDetachClass("pd_child", true))
					run(partitionConstraintEdge("pd_child_key", "pd_key", "pd_child"))
					run(fixture.Exec("ALTER TABLE pd_parent DETACH PARTITION pd_child", 0))
				})
			})
		}
	}
	partitionDetachOwnership(t)
	t.Run("if_exists", func(t *testing.T) {
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, prepared := range []bool{false, true} {
				notices = nil
				s := fixture.Exec("ALTER TABLE IF EXISTS pd_absent DETACH PARTITION also_absent", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, record, s)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "pd_absent" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices %#v want %#v", notices, want)
				}
			}
		})
	})
	t.Run("qualified_quoted", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "pd_names")
		partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA pd_names", 0))
			run(fixture.Exec(`CREATE TABLE pd_names."Parent"(k INT) PARTITION BY RANGE(k)`, 0))
			run(fixture.Exec(`CREATE TABLE pd_names."Child" PARTITION OF pd_names."Parent" DEFAULT`, 0))
			run(fixture.Prepared(fixture.Exec(`ALTER TABLE pd_names."Parent" DETACH PARTITION pd_names."Child"`, 0)))
			run(fixture.ImplicitError(`ALTER TABLE pd_names."Parent" DETACH PARTITION pd_names."Child"`, fixture.Diagnostic{Code: "42P01", Message: `relation "Child" is not a partition of relation "Parent"`}))
		})
	})
}

func partitionDetachOwnership(t *testing.T) {
	for _, authority := range []string{"parent_owner", "child_owner", "table_grant", "owner_before_kind", "schema_denied", "superuser"} {
		for _, prepared := range []bool{false, true} {
			name := authority + "/direct"
			if prepared {
				name = authority + "/held"
			}
			t.Run("ownership/"+name, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"pd_actor"})
				fixture.SchemaReferenceCleanup(t, "pd_private")
				partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					parentName, parentIndex := "pd_parent", "pd_key"
					if authority == "schema_denied" {
						run(fixture.Exec("CREATE SCHEMA pd_private", 0))
						parentName, parentIndex = "pd_private.pd_parent", "pd_private.pd_key"
					}
					partitionDetachSetup(run, parentName)
					run(fixture.Exec("CREATE ROLE pd_actor", 0))
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pd_actor", 0))
					} else {
						run(fixture.Exec("ALTER SCHEMA public OWNER TO pd_actor", 0))
					}
					q := "ALTER TABLE " + parentName + " DETACH PARTITION pd_child"
					want := fixture.Diagnostic{Code: "42501", Message: "must be owner of table pd_parent"}
					ok := authority == "parent_owner" || authority == "superuser"
					if authority == "parent_owner" {
						run(fixture.Exec("ALTER TABLE pd_parent OWNER TO pd_actor", 0))
						run(fixture.Exec("GRANT SELECT,INSERT ON pd_child TO pd_actor", 0))
					}
					if authority == "child_owner" {
						run(fixture.Exec("ALTER TABLE pd_child OWNER TO pd_actor", 0))
					}
					if authority == "table_grant" {
						run(fixture.Exec("GRANT ALL ON pd_parent,pd_child TO pd_actor", 0))
					}
					if authority == "owner_before_kind" {
						run(fixture.Exec("CREATE TABLE pd_regular(k INT)", 0))
						q = "ALTER TABLE pd_regular DETACH PARTITION pd_child"
						want = fixture.Diagnostic{Code: "42501", Message: "must be owner of table pd_regular"}
					}
					if authority == "schema_denied" {
						want = fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pd_private"}
					}
					identityQ := "SELECT oid::BIGINT,relname,relowner::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='pd_parent' OR relname='pd_child' OR relname='pd_child_key' ORDER BY relname"
					identities := partitionDetachCapture(t, p, conn, identityQ)
					var x fixture.Executor = conn
					if prepared {
						x = partitionDetachHold(t, p, conn, q)
					}
					if authority != "superuser" {
						run(fixture.Exec("SET ROLE pd_actor", 0))
						defer conn.ExecContext(p.Ctx, "RESET ROLE")
					}
					s := fixture.ImplicitError(q, want)
					if ok {
						s = fixture.Exec(q, 0)
					}
					t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) })
					if authority == "parent_owner" {
						run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2))
						run(fixture.Exec("INSERT INTO pd_child(payload,k) VALUES('acl',7)", 1))
					}
					run(fixture.Exec("RESET ROLE", 0))
					run(fixture.Select(identityQ, []string{"oid", "relname", "relowner", "reltype"}, []string{"INT8", "NAME", "INT8", "INT8"}, identities))
					run(partitionDetachClass("pd_child", !ok))
					if !ok {
						run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2))
						run(partitionConstraintEdge("pd_child_key", parentIndex, "pd_child"))
						run(fixture.Exec("ALTER TABLE "+parentName+" DETACH PARTITION pd_child", 0))
					}
				})
			})
		}
	}
}
