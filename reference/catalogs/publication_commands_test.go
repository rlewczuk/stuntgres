package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePublicationDefinitionOptions(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p", 0), publicationOptions("publication_rt_p", false, true, true, true, true, false, "n"))
		for _, tc := range []struct {
			value string
			want  bool
		}{{"true", true}, {"'ON'", true}, {"1", true}, {"false", false}, {"'off'", false}, {"0", false}, {"publish_via_partition_root", true}} {
			option := "publish_via_partition_root=" + tc.value
			if tc.value == "publish_via_partition_root" {
				option = tc.value
			}
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET("+option+")", 0), publicationOptions("publication_rt_p", false, true, true, true, true, tc.want, "n"))
		}
		run(fixture.Prepared(fixture.Exec("ALTER PUBLICATION publication_rt_p SET (publish='insert,truncate',publish_via_partition_root=true,publish_generated_columns=stored)", 0)), publicationOptions("publication_rt_p", false, true, false, false, true, true, "s"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET (publish='')", 0), publicationOptions("publication_rt_p", false, false, false, false, false, true, "s"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET (publish='update,update,delete',publish_via_partition_root=0)", 0), publicationOptions("publication_rt_p", false, false, true, true, false, false, "s"))
		cases := []struct{ query, state, message, detail string }{
			{"CREATE PUBLICATION publication_rt_p", "42710", `publication "publication_rt_p" already exists`, ""},
			{"ALTER PUBLICATION publication_rt_missing SET (publish='insert')", "42704", `publication "publication_rt_missing" does not exist`, ""},
			{"DROP PUBLICATION publication_rt_missing", "42704", `publication "publication_rt_missing" does not exist`, ""},
			{"ALTER PUBLICATION publication_rt_p SET (foo=true)", "42601", `unrecognized publication parameter: "foo"`, ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish='vacuum')", "42601", `unrecognized value for publication option "publish": "vacuum"`, ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish='insert,,delete')", "42601", `invalid list syntax in parameter "publish"`, ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish='insert',publish='delete')", "42601", "conflicting or redundant options", ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish)", "42601", "publish requires a parameter", ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish_via_partition_root='maybe')", "42601", "publish_via_partition_root requires a Boolean value", ""},
			{"ALTER PUBLICATION publication_rt_p SET (publish_generated_columns='virtual')", "42601", `invalid value for publication parameter "publish_generated_columns": "virtual"`, `Valid values are "none" and "stored".`},
		}
		for _, tc := range cases {
			s := publicationDiagnostic(tc.query, tc.state, tc.message, tc.detail, "")
			if tc.message == "conflicting or redundant options" {
				s.Diagnostic.Position = "58"
			}
			run(s, publicationOptions("publication_rt_p", false, false, true, true, false, false, "s"))
		}
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_publication WHERE pubname='publication_rt_p'").Scan(&oid); err != nil || oid <= 0 {
			t.Fatal("real publication identity", oid, err)
		}
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p RENAME TO publication_rt_renamed", 0), publicationOptions("publication_rt_renamed", false, false, true, true, false, false, "s"))
		run(fixture.Select("SELECT oid=$1::oid AS retained FROM pg_catalog.pg_publication WHERE pubname='publication_rt_renamed'", []string{"retained"}, []string{"BOOL"}, [][]any{{true}}, oid))
		run(publicationDiagnostic("DROP PUBLICATION publication_rt_renamed,publication_rt_missing", "42704", `publication "publication_rt_missing" does not exist`, "", ""), publicationOptions("publication_rt_renamed", false, false, true, true, false, false, "s"))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_second", 0), fixture.Exec("DROP PUBLICATION publication_rt_renamed,publication_rt_second RESTRICT", 0))
		run(fixture.Select("SELECT pubname FROM pg_catalog.pg_publication WHERE pubname LIKE 'publication_rt_%'", []string{"pubname"}, []string{"NAME"}, nil))
		publicationRelationBoundaries(t, p, b)
		publicationNotices(t, p, b)
	})
}

func TestReferencePublicationMembership(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE SCHEMA publication_rt_other", 0), fixture.Exec("CREATE TABLE publication_rt_s.a(id integer PRIMARY KEY)", 0), fixture.Exec("CREATE TABLE publication_rt_other.b(id integer PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_s.a, TABLES IN SCHEMA publication_rt_other", 0), publicationMembers("publication_rt_p", []any{"publication_rt_other", "b"}, []any{"publication_rt_s", "a"}), publicationNamespace("publication_rt_p", "publication_rt_other"))
		run(publicationExplicit("publication_rt_p", []any{"publication_rt_s", "a"}))
		run(fixture.Exec("CREATE TABLE publication_rt_s.future(id integer)", 0), fixture.Exec("CREATE TABLE publication_rt_other.future(id integer)", 0), publicationMembers("publication_rt_p", []any{"publication_rt_other", "b"}, []any{"publication_rt_other", "future"}, []any{"publication_rt_s", "a"}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_other.b", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_other", 0), publicationNamespace("publication_rt_p"), publicationMembers("publication_rt_p", []any{"publication_rt_other", "b"}, []any{"publication_rt_s", "a"}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLES IN SCHEMA publication_rt_s", 0), publicationNamespace("publication_rt_p", "publication_rt_s"), publicationMembers("publication_rt_p", []any{"publication_rt_s", "a"}, []any{"publication_rt_s", "future"}))
		run(publicationExplicit("publication_rt_p"))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.a", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_s.a", 0), publicationMembers("publication_rt_p", []any{"publication_rt_s", "a"}, []any{"publication_rt_s", "future"}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_other.b,publication_rt_other.b", 0), publicationNamespace("publication_rt_p"), publicationMembers("publication_rt_p", []any{"publication_rt_other", "b"}))
		run(publicationExplicit("publication_rt_p", []any{"publication_rt_other", "b"}))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_other.b", "42710", `relation "b" is already member of publication "publication_rt_p"`, "", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_s.a,publication_rt_other.b", "42710", `relation "b" is already member of publication "publication_rt_p"`, "", ""), publicationMembers("publication_rt_p", []any{"publication_rt_other", "b"}))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_s.a", "42704", `relation "a" is not part of the publication`, "", ""))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", "42710", `schema "publication_rt_s" is already member of publication "publication_rt_p"`, "", ""))
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_other", "42704", `tables from schema "publication_rt_other" are not part of the publication`, "", ""))
		c := publicationConn(t, p, b)
		defer c.Close()
		publicationRun(t, p, b, c, fixture.Exec("SET search_path=publication_rt_other", 0), fixture.Exec("CREATE PUBLICATION publication_rt_current FOR TABLES IN SCHEMA CURRENT_SCHEMA", 0), publicationNamespace("publication_rt_current", "publication_rt_other"), fixture.Exec("SET search_path='nonexistent'", 0), publicationDiagnostic("CREATE PUBLICATION publication_rt_bad FOR TABLES IN SCHEMA CURRENT_SCHEMA", "3F000", "no schema has been selected for CURRENT_SCHEMA", "", ""), fixture.Exec("RESET search_path", 0))
	})
}

func TestReferencePublicationInheritancePartitions(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
		run(fixture.Exec("CREATE SCHEMA publication_rt_s", 0), fixture.Exec("CREATE SCHEMA publication_rt_other", 0), fixture.Exec("CREATE TABLE publication_rt_s.parent(id integer)", 0), fixture.Exec("CREATE TABLE publication_rt_s.child() INHERITS(publication_rt_s.parent)", 0))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_only FOR TABLE ONLY publication_rt_s.parent", 0), fixture.Exec("CREATE PUBLICATION publication_rt_tree FOR TABLE publication_rt_s.parent *", 0), publicationMembers("publication_rt_only", []any{"publication_rt_s", "parent"}), publicationMembers("publication_rt_tree", []any{"publication_rt_s", "child"}, []any{"publication_rt_s", "parent"}))
		run(fixture.Exec("CREATE TABLE publication_rt_s.later() INHERITS(publication_rt_s.parent)", 0), publicationMembers("publication_rt_tree", []any{"publication_rt_s", "child"}, []any{"publication_rt_s", "parent"}))
		run(publicationExplicit("publication_rt_only", []any{"publication_rt_s", "parent"}), publicationExplicit("publication_rt_tree", []any{"publication_rt_s", "child"}, []any{"publication_rt_s", "parent"}))
		run(fixture.Exec("CREATE TABLE publication_rt_s.root(id integer PRIMARY KEY) PARTITION BY RANGE(id)", 0), fixture.Exec("CREATE TABLE publication_rt_other.leaf PARTITION OF publication_rt_s.root FOR VALUES FROM(0) TO(10)", 0), fixture.Exec("CREATE PUBLICATION publication_rt_partition FOR TABLE publication_rt_s.root", 0), publicationMembers("publication_rt_partition", []any{"publication_rt_other", "leaf"}))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_partitiononly FOR TABLE ONLY publication_rt_s.root", 0), publicationMembers("publication_rt_partitiononly", []any{"publication_rt_other", "leaf"}))
		run(fixture.Exec("CREATE TABLE publication_rt_s.next PARTITION OF publication_rt_s.root FOR VALUES FROM(10) TO(20)", 0), publicationMembers("publication_rt_partition", []any{"publication_rt_other", "leaf"}, []any{"publication_rt_s", "next"}))
		run(publicationExplicit("publication_rt_partition", []any{"publication_rt_s", "root"}), publicationMembers("publication_rt_partitiononly", []any{"publication_rt_other", "leaf"}, []any{"publication_rt_s", "next"}))
		run(fixture.Exec("ALTER PUBLICATION publication_rt_partition SET (publish_via_partition_root=true)", 0), publicationMembers("publication_rt_partition", []any{"publication_rt_s", "root"}))
		run(fixture.Exec("ALTER TABLE publication_rt_s.root DETACH PARTITION publication_rt_s.next", 0), publicationMembers("publication_rt_partition", []any{"publication_rt_s", "root"}), fixture.Exec("ALTER PUBLICATION publication_rt_partition SET (publish_via_partition_root=false)", 0), publicationMembers("publication_rt_partition", []any{"publication_rt_other", "leaf"}))
		run(fixture.Exec("CREATE UNLOGGED TABLE publication_rt_s.unlogged(id integer)", 0), fixture.Exec("CREATE VIEW publication_rt_s.v AS SELECT 1 AS id", 0), fixture.Exec("CREATE SEQUENCE publication_rt_s.seq", 0), fixture.Exec("CREATE TEMP TABLE publication_rt_temp(id integer)", 0))
		run(fixture.Exec("CREATE PUBLICATION publication_rt_all FOR ALL TABLES", 0), publicationOptions("publication_rt_all", true, true, true, true, true, false, "n"), publicationNamespace("publication_rt_all"))
		run(publicationExplicit("publication_rt_all"))
		run(publicationMembers("publication_rt_all", []any{"publication_rt_other", "leaf"}, []any{"publication_rt_s", "child"}, []any{"publication_rt_s", "later"}, []any{"publication_rt_s", "next"}, []any{"publication_rt_s", "parent"}))
		run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_all' AND tablename IN ('unlogged','v','seq','publication_rt_temp')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE TABLE publication_rt_s.future(id integer)", 0), fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_all' AND schemaname='publication_rt_s' AND tablename='future'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		for _, kind := range []string{"TABLE publication_rt_s.parent", "TABLES IN SCHEMA publication_rt_s"} {
			detail := "Tables cannot be added to or dropped from FOR ALL TABLES publications."
			if kind == "TABLES IN SCHEMA publication_rt_s" {
				detail = "Schemas cannot be added to or dropped from FOR ALL TABLES publications."
			}
			for _, action := range []string{"ADD", "DROP", "SET"} {
				run(publicationDiagnostic("ALTER PUBLICATION publication_rt_all "+action+" "+kind, "55000", `publication "publication_rt_all" is defined as FOR ALL TABLES`, detail, ""))
			}
		}
	})
}
