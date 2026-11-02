package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyKinds(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pkey_kind_reader"})
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE pkey_kind_heap(id integer PRIMARY KEY,payload text)",
			"CREATE INDEX pkey_kind_index ON pkey_kind_heap(payload)",
			"CREATE TYPE pkey_kind_composite AS(id integer)",
			"CREATE TABLE pkey_kind_parent(bucket integer,id integer,PRIMARY KEY(id,bucket)) PARTITION BY RANGE(bucket)",
			"CREATE TABLE pkey_kind_leaf PARTITION OF pkey_kind_parent DEFAULT",
			"CREATE VIEW pkey_kind_view AS SELECT id,payload FROM pkey_kind_heap",
			"CREATE SEQUENCE pkey_kind_sequence",
			"CREATE ROLE pkey_kind_reader",
			"GRANT USAGE ON SCHEMA public,pk_ext TO pkey_kind_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO pkey_kind_heap VALUES(7,'heap')", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW pkey_kind_materialized AS SELECT id,payload FROM pkey_kind_heap", 1))
		held := pkeyHeld(t, p, c, pkeyCall)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// REL_18_6 table_open rejects these kinds before get_rel_from_relname
		// checks SELECT. The role has no relation grants: ACL must not win.
		for _, role := range []string{"owner", "unprivileged"} {
			if role == "unprivileged" {
				run(fixture.Exec("SET SESSION AUTHORIZATION pkey_kind_reader", 0))
			}
			t.Run(role, func(t *testing.T) {
				for _, tc := range []struct{ relation, detail string }{
					{"pkey_kind_index", "This operation is not supported for indexes."},
					{"pkey_kind_composite", "This operation is not supported for composite types."},
					{"pkey_kind_parent_pkey", "This operation is not supported for partitioned indexes."},
				} {
					t.Run(tc.relation, func(t *testing.T) {
						s := pkeyError(pkeyCall, "42809", `cannot open relation "`+tc.relation+`"`, tc.relation)
						s.Diagnostic.Detail = tc.detail
						pkeyRoutes(t, p, b, c, &held, s)
					})
				}
			})
			if role == "unprivileged" {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		run(fixture.Exec("GRANT SELECT ON pkey_kind_heap,pkey_kind_parent,pkey_kind_leaf,pkey_kind_view,pkey_kind_materialized TO pkey_kind_reader", 0))
		run(fixture.Exec("GRANT SELECT ON SEQUENCE pkey_kind_sequence TO pkey_kind_reader", 0))
		for _, role := range []string{"owner", "reader"} {
			if role == "reader" {
				run(fixture.Exec("SET SESSION AUTHORIZATION pkey_kind_reader", 0))
			}
			t.Run(role+"_supported", func(t *testing.T) {
				for _, tc := range []struct {
					relation string
					rows     [][]any
				}{
					{"pkey_kind_heap", [][]any{{int64(1), "id"}}},
					{"pkey_kind_parent", [][]any{{int64(1), "id"}, {int64(2), "bucket"}}},
					{"pkey_kind_leaf", [][]any{{int64(1), "id"}, {int64(2), "bucket"}}},
					{"pkey_kind_view", nil},
					{"pkey_kind_materialized", nil},
					{"pkey_kind_sequence", nil},
				} {
					t.Run(tc.relation, func(t *testing.T) {
						s := pkeyRows(tc.rows, tc.relation)
						s.CheckMetadata = pkeyMetadata(b.PG)
						pkeyRoutes(t, p, b, c, &held, s)
					})
				}
			})
			if role == "reader" {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		run(fixture.Exec("INSERT INTO pkey_kind_parent VALUES(3,19)", 1))
		t.Run("partition_data_independent", func(t *testing.T) {
			pkeyRoutes(t, p, b, c, &held, pkeyRows([][]any{{int64(1), "id"}, {int64(2), "bucket"}}, "pkey_kind_parent"))
		})
		run(fixture.Select("SELECT bucket,id FROM pkey_kind_parent ORDER BY bucket,id", []string{"bucket", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(19)}}))
	})
}
