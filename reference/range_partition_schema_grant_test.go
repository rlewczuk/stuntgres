package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Schema expansion must include virtual roots and intermediate partitions, not
// merely their physical leaves. Each direction starts from a fresh catalog so
// a missing GRANT cannot conceal a schema-wide REVOKE that leaves access behind.
func TestReferenceRangePartitionSchemaGrantExpansion(t *testing.T) {
	for _, direction := range []string{"grant_current", "revoke_explicit"} {
		t.Run(direction, func(t *testing.T) {
			roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Errorf("reset schema-grant actor: %v", err)
					}
				}()
				for _, q := range []string{
					"CREATE TABLE rp_grant_root(k INT) PARTITION BY RANGE(k)",
					"CREATE TABLE rp_grant_leaf PARTITION OF rp_grant_root FOR VALUES FROM(1) TO(3)",
					"CREATE TABLE rp_grant_middle PARTITION OF rp_grant_root DEFAULT PARTITION BY RANGE(k)",
					"CREATE TABLE rp_grant_deep PARTITION OF rp_grant_middle DEFAULT",
					"CREATE TABLE rp_grant_plain(k INT)",
					"CREATE SEQUENCE rp_grant_seq START WITH 20",
					"CREATE VIEW rp_grant_view AS SELECT k FROM rp_grant_plain",
				} {
					run(referenceExec(q, 0))
				}
				run(referenceExec("INSERT INTO rp_grant_root VALUES(1),(3)", 2))
				run(referenceExec("INSERT INTO rp_grant_plain VALUES(9)", 1))
				run(referenceExec("CREATE MATERIALIZED VIEW rp_grant_materialized AS SELECT k FROM rp_grant_plain", 1))

				// These projections specify names, types and rows independently on
				// every backend. Declared logical/physical table columns additionally
				// exercise the existing complete DDL metadata expectations.
				rows := func(target string, values ...int64) referenceStep {
					s := referenceRangeRows("SELECT k FROM "+target+" ORDER BY k", values...)
					if target != "rp_grant_view" && target != "rp_grant_materialized" {
						s.checkMetadata = referenceRangeMetadata(b.pg, []string{"k"}, []string{"INT4"}, []bool{true})
					}
					return s
				}
				current := []struct {
					name string
					seed []int64
				}{
					{"rp_grant_root", []int64{1, 3}},
					{"rp_grant_middle", []int64{3}},
					{"rp_grant_leaf", []int64{1}},
					{"rp_grant_deep", []int64{3}},
					{"rp_grant_plain", []int64{9}},
					{"rp_grant_view", []int64{9}},
					{"rp_grant_materialized", []int64{9}},
				}
				if direction == "grant_current" {
					// Repeated schema names retain the baseline duplicate-target
					// contract. This grants existing relations, not future ones.
					run(referenceExec("GRANT SELECT,INSERT ON ALL TABLES IN SCHEMA public,public TO rg_reader", 0))
				} else {
					// Named grants establish access independently of the defective
					// schema expansion, including both virtual relations.
					run(referenceExec("GRANT SELECT,INSERT ON rp_grant_root,rp_grant_middle,rp_grant_leaf,rp_grant_deep,rp_grant_plain,rp_grant_view,rp_grant_materialized TO rg_reader", 0))
				}
				run(referenceExec("CREATE TABLE rp_grant_later(k INT)", 0))
				run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
				for _, relation := range current {
					run(rows(relation.name, relation.seed...))
				}
				for _, q := range []string{
					"INSERT INTO rp_grant_root VALUES(2)",
					"INSERT INTO rp_grant_middle VALUES(4)",
					"INSERT INTO rp_grant_leaf VALUES(2)",
					"INSERT INTO rp_grant_deep VALUES(5)",
					"INSERT INTO rp_grant_plain VALUES(10)",
				} {
					run(referenceExec(q, 1))
				}
				assertData := func() {
					run(rows("rp_grant_root", 1, 2, 2, 3, 4, 5))
					run(rows("rp_grant_middle", 3, 4, 5))
					run(rows("rp_grant_leaf", 1, 2, 2))
					run(rows("rp_grant_deep", 3, 4, 5))
					run(rows("rp_grant_plain", 9, 10))
					// The ordinary view remains live; the populated materialized
					// view retains its snapshot. No new writable-view capability
					// is implied by granting the existing table privilege bits.
					run(rows("rp_grant_view", 9, 10))
					run(rows("rp_grant_materialized", 9))
				}
				assertData()
				run(roleGrantDenied("SELECT k FROM rp_grant_later", "rp_grant_later"))
				run(roleGrantDenied("INSERT INTO rp_grant_later VALUES(11)", "rp_grant_later"))
				run(referenceImplicitError("SELECT nextval('rp_grant_seq')", referenceDiagnostic{Code: "42501", Message: "permission denied for sequence rp_grant_seq"}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))

				// Both directions must remove current named-target authority.
				// In particular, leaf revocation cannot substitute for revoking
				// the root: internal routing and scanning use the named target.
				run(referenceExec("REVOKE SELECT,INSERT ON ALL TABLES IN SCHEMA public,public FROM rg_reader", 0))
				run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
				for _, relation := range current {
					kind := "table"
					switch relation.name {
					case "rp_grant_view":
						kind = "view"
					case "rp_grant_materialized":
						kind = "materialized view"
					}
					run(referenceImplicitError("SELECT k FROM "+relation.name, referenceDiagnostic{Code: "42501", Message: "permission denied for " + kind + " " + relation.name}))
				}
				for _, attempt := range []struct {
					name  string
					value string
				}{
					{"rp_grant_root", "6"},
					{"rp_grant_middle", "7"},
					{"rp_grant_leaf", "2"},
					{"rp_grant_deep", "8"},
					{"rp_grant_plain", "11"},
				} {
					run(roleGrantDenied("INSERT INTO "+attempt.name+" VALUES("+attempt.value+")", attempt.name))
				}
				run(roleGrantDenied("SELECT k FROM rp_grant_later", "rp_grant_later"))
				run(roleGrantDenied("INSERT INTO rp_grant_later VALUES(11)", "rp_grant_later"))
				run(referenceImplicitError("SELECT nextval('rp_grant_seq')", referenceDiagnostic{Code: "42501", Message: "permission denied for sequence rp_grant_seq"}))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				assertData()
				run(rows("rp_grant_later"))
				run(referenceSelect("SELECT last_value,is_called FROM rp_grant_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), false}}))
			})
		})
	}
}
