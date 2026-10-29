package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePublicationDeferredIdentity(t *testing.T) {
	t.Run("held_execution", publicationHeldIdentity)
	// Each leaf has a fresh context and independent expected outcome. Definition
	// succeeds even when UPDATE/DELETE admission will reject its identity coverage.
	cases := []struct{ name, ddl, identity, columns, filter, actions, reason string }{
		{"default_pk", "PRIMARY KEY(id)", "DEFAULT", "", "id>0", "update,delete", ""},
		{"default_no_pk", "CHECK(id>0)", "DEFAULT", "", "", "update,delete", "missing"},
		{"default_no_pk_list", "CHECK(id>0)", "DEFAULT", "(v)", "true", "update,delete", "missing"},
		{"default_nonkey_filter", "PRIMARY KEY(id)", "DEFAULT", "", "v>0", "update,delete", "filter"},
		{"default_no_pk_filter", "CHECK(id>0)", "DEFAULT", "", "v>0", "update,delete", "filter"},
		{"default_missing_key", "PRIMARY KEY(id)", "DEFAULT", "(v)", "", "update,delete", "columns"},
		{"full_filter", "PRIMARY KEY(id)", "FULL", "", "v>0", "update,delete", ""},
		{"full_explicit_all", "PRIMARY KEY(id)", "FULL", "(id,v)", "", "update,delete", "columns"},
		{"nothing", "PRIMARY KEY(id)", "NOTHING", "", "", "update,delete", "missing"},
		{"nothing_filter", "PRIMARY KEY(id)", "NOTHING", "", "id>0", "update,delete", "filter"},
		{"selected_key", "PRIMARY KEY(id)", "USING INDEX publication_rt_idx", "(v)", "v>0", "update,delete", ""},
		{"selected_not_pk", "PRIMARY KEY(id)", "USING INDEX publication_rt_idx", "(id)", "", "update,delete", "columns"},
		{"selected_include_not_key", "PRIMARY KEY(id)", "USING INDEX publication_rt_idx", "", "id>0", "update,delete", "filter"},
		{"insert_only", "CHECK(id>0)", "NOTHING", "(v)", "v>0", "insert", ""},
		{"truncate_only", "CHECK(id>0)", "NOTHING", "(v)", "v>0", "truncate", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
				run(fixture.Exec("CREATE TABLE publication_rt_t(id integer NOT NULL,v integer NOT NULL,"+tc.ddl+")", 0), fixture.Exec("CREATE UNIQUE INDEX publication_rt_idx ON publication_rt_t(v) INCLUDE(id)", 0), fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY "+tc.identity, 0), fixture.Exec("INSERT INTO publication_rt_t VALUES(1,10)", 1))
				where := ""
				if tc.filter != "" {
					where = " WHERE (" + tc.filter + ")"
				}
				run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t"+tc.columns+where+" WITH(publish='"+tc.actions+"')", 0))
				for _, prepared := range []bool{false, true} {
					for _, op := range []string{"update", "delete"} {
						queries := []string{"UPDATE publication_rt_t SET v=v WHERE false", "UPDATE publication_rt_t SET v=v WHERE id=1"}
						if op == "delete" {
							queries = []string{"DELETE FROM publication_rt_t WHERE false", "DELETE FROM publication_rt_t WHERE id=999"}
						}
						for i, q := range queries {
							var s fixture.Step
							if tc.reason == "" {
								count := int64(0)
								if op == "update" && i == 1 {
									count = 1
								}
								s = fixture.Exec(q, count)
							} else {
								s = publicationIdentityError(q, op, "publication_rt_t", tc.reason)
							}
							s.Prepared = prepared
							run(s)
						}
					}
				}
				run(fixture.Select("SELECT id,v FROM publication_rt_t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}), fixture.Exec("TRUNCATE publication_rt_t", 0))
			})
		})
	}
	t.Run("action_admission", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
			run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer)", 0), fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY NOTHING", 0), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t", 0))
			for _, q := range []string{
				"INSERT INTO publication_rt_t VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=excluded.v",
				"MERGE INTO publication_rt_t t USING (VALUES(1,10)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)",
				"MERGE INTO publication_rt_t t USING (SELECT 1 AS id,10 AS v WHERE false) s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v",
			} {
				run(publicationIdentityError(q, "update", "publication_rt_t", "missing"))
			}
			run(publicationIdentityError("MERGE INTO publication_rt_t t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE", "delete", "publication_rt_t", "missing"))
			run(fixture.Exec("INSERT INTO publication_rt_t VALUES(1,10) ON CONFLICT(id) DO NOTHING", 1))
			// One permissive publication must not erase another's restrictions.
			run(fixture.Exec("CREATE PUBLICATION publication_rt_second FOR TABLE publication_rt_t WITH(publish='insert')", 0), publicationIdentityError("UPDATE publication_rt_t SET v=20 WHERE false", "update", "publication_rt_t", "missing"))
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='delete')", 0), fixture.Exec("UPDATE publication_rt_t SET v=20", 1), publicationIdentityError("DELETE FROM publication_rt_t WHERE false", "delete", "publication_rt_t", "missing"))
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='update')", 0), fixture.Exec("DELETE FROM publication_rt_t", 1), publicationIdentityError("UPDATE publication_rt_t SET v=20 WHERE false", "update", "publication_rt_t", "missing"))
		})
	})
	t.Run("partition_leaf_identity", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
			run(fixture.Exec("CREATE TABLE publication_rt_root(id integer PRIMARY KEY,v integer) PARTITION BY RANGE(id)", 0), fixture.Exec("CREATE TABLE publication_rt_leaf(v integer,id integer PRIMARY KEY)", 0), fixture.Exec("ALTER TABLE publication_rt_root ATTACH PARTITION publication_rt_leaf FOR VALUES FROM(0) TO(10)", 0), fixture.Exec("ALTER TABLE publication_rt_root REPLICA IDENTITY FULL", 0), fixture.Exec("INSERT INTO publication_rt_root VALUES(1,10)", 1), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_root WHERE(v>0) WITH(publish_via_partition_root=true)", 0))
			run(publicationIdentityError("UPDATE publication_rt_root SET v=20 WHERE id=1", "update", "publication_rt_leaf", "filter"), publicationIdentityError("DELETE FROM publication_rt_leaf WHERE id=1", "delete", "publication_rt_leaf", "filter"))
			run(publicationIdentityError("INSERT INTO publication_rt_root VALUES(2,10) ON CONFLICT(id) DO UPDATE SET v=excluded.v", "update", "publication_rt_leaf", "filter"), publicationIdentityError("MERGE INTO publication_rt_root t USING(VALUES(1,20)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v", "update", "publication_rt_leaf", "filter"))
			run(fixture.Select("SELECT id,v FROM publication_rt_root", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			// FULL admits the filter against the physical leaf. Keep this witness
			// about publication state and supported UPDATE, not hierarchy DELETE.
			run(fixture.Exec("ALTER TABLE publication_rt_leaf REPLICA IDENTITY FULL", 0), fixture.Exec("UPDATE publication_rt_root SET v=20 WHERE id=1", 1))
			run(fixture.Select("SELECT id,v FROM publication_rt_root", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(20)}}))
			run(fixture.Select("SELECT pubupdate,pubdelete,pubviaroot FROM pg_publication WHERE pubname='publication_rt_p'", []string{"pubupdate", "pubdelete", "pubviaroot"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			run(fixture.Select("SELECT relreplident FROM pg_class WHERE relname='publication_rt_leaf'", []string{"relreplident"}, []string{"CHAR"}, [][]any{{"f"}}))
		})
	})
	t.Run("generated_identity", func(t *testing.T) {
		publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
			run(fixture.Exec("CREATE TABLE publication_rt_t(id integer,g integer GENERATED ALWAYS AS(id+1) STORED PRIMARY KEY)", 0), fixture.Exec("INSERT INTO publication_rt_t(id) VALUES(1)", 1), fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t", 0), publicationIdentityError("UPDATE publication_rt_t SET id=2 WHERE false", "update", "publication_rt_t", "generated"))
			run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish_generated_columns=stored)", 0), fixture.Exec("UPDATE publication_rt_t SET id=2", 1), fixture.Exec("DELETE FROM publication_rt_t", 1))
		})
	})
}

func publicationIdentityError(q, op, table, reason string) fixture.Step {
	verb := "update table"
	if op == "delete" {
		verb = "delete from table"
	}
	message := "cannot " + verb + ` "` + table + `"`
	if reason == "missing" {
		gerund, action := "updating", "updates"
		if op == "delete" {
			gerund, action = "deleting from", "deletes"
		}
		s := publicationDiagnostic(q, "55000", message+" because it does not have a replica identity and publishes "+action, "", "To enable "+gerund+" the table, set REPLICA IDENTITY using ALTER TABLE.")
		s.ErrorStage = "execute"
		return s
	}
	detail := "Column used in the publication WHERE expression is not part of the replica identity."
	if reason == "columns" {
		detail = "Column list used by the publication does not cover the replica identity."
	}
	if reason == "generated" {
		detail = "Replica identity must not contain unpublished generated columns."
	}
	s := publicationDiagnostic(q, "42P10", message, detail, "")
	s.ErrorStage = "execute"
	return s
}

// FULL includes every physical identity attribute, but virtual generated columns
// can never be published, including under publish_generated_columns=stored.
func TestReferencePublicationFullVirtualIdentity(t *testing.T) {
	for _, identity := range []string{"DEFAULT", "FULL"} {
		for _, policy := range []string{"none", "stored"} {
			for _, actions := range []string{"update,delete", "update", "delete", "insert"} {
				t.Run(identity+"/"+policy+"/"+actions, func(t *testing.T) {
					publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						run := func(s ...fixture.Step) { publicationRun(t, p, b, b.DB, s...) }
						run(fixture.Exec("CREATE TABLE publication_rt_t(id integer PRIMARY KEY,v integer GENERATED ALWAYS AS(id+1) VIRTUAL)", 0),
							fixture.Exec("ALTER TABLE publication_rt_t REPLICA IDENTITY "+identity, 0),
							fixture.Exec("INSERT INTO publication_rt_t(id) VALUES(1)", 1),
							fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_t WITH(publish='"+actions+"',publish_generated_columns="+policy+")", 0))
						for _, prepared := range []bool{false, true} {
							for _, op := range []string{"update", "delete"} {
								queries := []string{"UPDATE publication_rt_t SET id=id WHERE false", "UPDATE publication_rt_t SET id=id WHERE id=1"}
								if op == "delete" {
									queries = []string{"DELETE FROM publication_rt_t WHERE false", "DELETE FROM publication_rt_t WHERE id=999"}
								}
								published := actions == "update,delete" || actions == op
								for i, query := range queries {
									step := fixture.Exec(query, 0)
									if identity == "FULL" && published {
										step = publicationIdentityError(query, op, "publication_rt_t", "generated")
									} else if op == "update" && i == 1 {
										step = fixture.Exec(query, 1)
									}
									step.Prepared = prepared
									run(step)
								}
							}
						}
						run(fixture.Select("SELECT id,v FROM publication_rt_t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					})
				})
			}
		}
	}
}
