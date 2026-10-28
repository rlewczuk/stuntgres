package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentConstraints(t *testing.T) {
	for _, kind := range []string{"range", "list", "list_null", "hash", "stronger", "ordinary", "subtree"} {
		for _, held := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					strategy, bound := "RANGE(k)", "FROM(2) TO(4)"
					if kind == "list" {
						strategy, bound = "LIST(k)", "IN(2,3)"
					}
					if kind == "list_null" {
						strategy, bound = "LIST(k)", "IN(2,NULL)"
					}
					if kind == "hash" {
						strategy, bound = "HASH(k)", "WITH(MODULUS 1,REMAINDER 0)"
					}
					run(fixture.Exec("CREATE SEQUENCE dc_owned", 0))
					run(fixture.Exec("CREATE TABLE dc_parent(k INT,payload TEXT DEFAULT 'saved',n INT NOT NULL DEFAULT nextval('dc_owned'),CONSTRAINT dc_payload_check CHECK(payload<>'bad')) PARTITION BY "+strategy, 0))
					run(fixture.Exec("CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES "+bound+map[bool]string{false: "", true: " PARTITION BY RANGE(k)"}[kind == "subtree"], 0))
					if kind == "subtree" {
						run(fixture.Exec("CREATE TABLE dc_leaf PARTITION OF dc_leave FOR VALUES FROM(2) TO(4)", 0))
					}
					if kind == "stronger" {
						run(fixture.Exec("ALTER TABLE dc_leave ADD CONSTRAINT dc_strong CHECK(k IS NOT NULL AND k=2)", 0))
					}
					run(fixture.Exec("CREATE UNIQUE INDEX dc_key ON dc_parent(k)", 0))
					run(fixture.Exec("CREATE UNIQUE INDEX dc_plain ON dc_parent(k,payload)", 0))
					if kind == "subtree" {
						run(fixture.Exec("CREATE UNIQUE INDEX dc_local ON dc_leave(k,n)", 0))
						run(fixture.Exec("CREATE INDEX dc_leaf_local ON dc_leaf(n)", 0))
					} else {
						run(fixture.Exec("CREATE INDEX dc_local ON dc_leave(n)", 0))
					}
					run(fixture.Exec("CREATE VIEW dc_view AS SELECT k,payload FROM dc_leave", 0))
					run(fixture.Exec("INSERT INTO dc_parent(k) VALUES(2)", 1))
					mode := " CONCURRENTLY"
					if kind == "ordinary" {
						mode = ""
					}
					detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave"+mode, held)
					run(detachConcurrentGraph("dc_parent", "dc_leave", false, false))
					run(partitionDetachTopEdges("dc_leave_k_idx", false))
					run(partitionConstraintIndexState("dc_key", true))
					if kind == "subtree" {
						run(partitionConstraintIndexState("dc_local", true))
						run(detachConcurrentPlainIndex("dc_leaf_local"))
					} else {
						run(detachConcurrentPlainIndex("dc_local"))
					}
					if kind == "subtree" {
						run(detachConcurrentGraph("dc_leave", "dc_leaf", true, false))
						run(partitionConstraintEdge("dc_leaf_k_idx", "dc_leave_k_idx", "dc_leaf"))
					}
					checks := int64(2)
					if kind == "hash" || kind == "ordinary" {
						checks = 1
					}
					run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND contype='c' AND convalidated AND conenforced AND conislocal", []string{"count"}, []string{"INT8"}, [][]any{{checks}}))
					// Native and oracle independently require the same executable bound; the
					// generated CHECK spelling is separately asserted below for simple RANGE.
					if kind == "range" {
						run(fixture.Select("SELECT conname,pg_get_expr(conbin,conrelid) AS expression FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND contype='c' AND conname<>'dc_payload_check'", []string{"conname", "expression"}, []string{"NAME", "TEXT"}, [][]any{{"dc_leave_k_check", "((k IS NOT NULL) AND (k >= 2) AND (k < 4))"}}))
					}
					if kind == "range" || kind == "list" || kind == "stronger" {
						for _, value := range []string{"NULL", "9"} {
							q := "INSERT INTO dc_leave(k) VALUES(" + value + ")"
							name := "dc_leave_k_check"
							if kind == "stronger" {
								name = "dc_strong"
							}
							// Explicit n avoids sequence-dependent failure detail.
							q = "INSERT INTO dc_leave(k,payload,n) VALUES(" + value + ",'saved',99)"
							detail := "Failing row contains (" + value + ", saved, 99)."
							if value == "NULL" {
								detail = "Failing row contains (null, saved, 99)."
							}
							run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: `new row for relation "dc_leave" violates check constraint "` + name + `"`, Detail: detail, Schema: "public", Table: "dc_leave", Constraint: name}))
						}
					}
					if kind == "list_null" {
						run(fixture.Exec("INSERT INTO dc_leave(k,payload,n) VALUES(NULL,'saved',99)", 1))
					}
					if kind == "hash" || kind == "ordinary" {
						run(fixture.Exec("INSERT INTO dc_leave(k,payload,n) VALUES(9,'saved',99)", 1))
					}
					run(partitionConstraintDuplicate("INSERT INTO dc_leave(k,payload,n) VALUES(2,'saved',99)", map[bool]string{false: "dc_leave", true: "dc_leaf"}[kind == "subtree"], map[bool]string{false: "dc_leave_k_idx", true: "dc_leaf_k_idx"}[kind == "subtree"], "Key (k)=(2) already exists."))
					if kind != "subtree" {
						run(fixture.ImplicitError("INSERT INTO dc_leave(k,payload,n) VALUES(2,'bad',99)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "dc_leave" violates check constraint "dc_payload_check"`, Detail: "Failing row contains (2, bad, 99).", Schema: "public", Table: "dc_leave", Constraint: "dc_payload_check"}))
						run(fixture.ImplicitError("INSERT INTO dc_leave(k,payload,n) VALUES(2,'saved',NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "dc_leave" violates not-null constraint`, Detail: "Failing row contains (2, saved, null).", Schema: "public", Table: "dc_leave", Column: "n"}))
					}
					run(fixture.Exec("DROP TABLE dc_parent", 0))
					run(fixture.Select("SELECT payload FROM dc_view WHERE k=2", []string{"payload"}, []string{"TEXT"}, [][]any{{"saved"}}))
					run(fixture.Select("SELECT (ROW(2,'saved',1)::dc_leave).k AS k", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}))
					run(fixture.Exec("DROP VIEW dc_view", 0))
					run(fixture.Exec("DROP TABLE dc_leave", 0))
					run(fixture.Exec("DROP SEQUENCE dc_owned", 0))
				})
			})
		}
	}
	detachConcurrentAncestry(t)
}
