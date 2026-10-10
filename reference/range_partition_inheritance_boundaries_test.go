package reference_test

import "testing"

// REL_18_0 parse_utilcmd.c transformCreateStmt rejects an inheritance child
// with a partition key. tablecmds.c MergeAttributes separately rejects a
// partitioned parent and a physical partition before merging its columns.
// These complete diagnostic predictions await the root's pinned oracle run;
// none are derived from native errors or a successful baseline-only check.
func TestReferenceRangePartitionInheritanceBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, suffix, code, message string
	}{
		{"parents_with_partition_key", "INHERITS (rp_inherit_plain) PARTITION BY RANGE(k)", "42P17", "cannot create partitioned table as inheritance child"},
		{"partitioned_parent", "INHERITS (rp_inherit_root)", "42809", `cannot inherit from partitioned table "rp_inherit_root"`},
		{"physical_partition_parent", "INHERITS (rp_inherit_leaf)", "42809", `cannot inherit from partition "rp_inherit_leaf"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				for _, query := range []string{
					"CREATE SEQUENCE rp_inherit_draw START 20",
					"CREATE TABLE rp_inherit_plain(k INT,payload TEXT DEFAULT 'plain-default')",
					"CREATE TABLE rp_inherit_root(k INT,payload TEXT) PARTITION BY RANGE(k)",
					"CREATE TABLE rp_inherit_leaf PARTITION OF rp_inherit_root FOR VALUES FROM(1) TO(3)",
					"CREATE TABLE rp_inherit_mid PARTITION OF rp_inherit_root FOR VALUES FROM(3) TO(5) PARTITION BY RANGE(k)",
					"CREATE TABLE rp_inherit_deep PARTITION OF rp_inherit_mid DEFAULT",
				} {
					run(referenceExec(query, 0))
				}
				run(referenceExec("INSERT INTO rp_inherit_plain VALUES(9,'ordinary-sentinel')", 1))
				run(referenceExec("INSERT INTO rp_inherit_root VALUES(1,'low-sentinel'),(3,'nested-sentinel')", 2))

				// OIDs are observed independently per backend, then required to
				// retain identity; diagnostic and data goldens remain literals.
				names := []string{"rp_inherit_plain", "rp_inherit_root", "rp_inherit_leaf", "rp_inherit_mid", "rp_inherit_deep", "rp_inherit_draw"}
				identities := make(map[string]int64, len(names))
				for _, name := range names {
					var oid int64
					if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='"+name+"'").Scan(&oid); err != nil || oid <= 0 {
						t.Fatalf("capturing %s identity: oid=%d err=%v", name, oid, err)
					}
					identities[name] = oid
				}
				unchanged := func(t *testing.T) {
					t.Helper()
					for _, name := range names {
						var oid int64
						if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='"+name+"'").Scan(&oid); err != nil || oid != identities[name] {
							t.Fatalf("retaining %s identity: got=%d want=%d err=%v", name, oid, identities[name], err)
						}
					}
					for _, source := range []struct {
						name string
						rows [][]any
					}{
						{"rp_inherit_plain", [][]any{{int64(9), "ordinary-sentinel"}}},
						{"rp_inherit_root", [][]any{{int64(1), "low-sentinel"}, {int64(3), "nested-sentinel"}}},
						{"rp_inherit_leaf", [][]any{{int64(1), "low-sentinel"}}},
						{"rp_inherit_mid", [][]any{{int64(3), "nested-sentinel"}}},
						{"rp_inherit_deep", [][]any{{int64(3), "nested-sentinel"}}},
					} {
						for _, prepared := range []bool{false, true} {
							s := referenceSelect("SELECT k,payload FROM "+source.name+" ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, source.rows)
							s.prepared = prepared
							s.checkMetadata = referenceRangeMetadata(b.pg, []string{"k", "payload"}, []string{"INT4", "TEXT"}, []bool{true, true})
							referenceRunStep(t, p.ctx, b.db, b.recorder, s)
						}
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT last_value,is_called FROM rp_inherit_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(20), false}}))
				}
				unchanged(t)
				for _, route := range []struct {
					name     string
					prepared bool
				}{{"direct", false}, {"prepared", true}} {
					if !t.Run(route.name, func(t *testing.T) {
						bad := "rp_inherit_bad_" + route.name
						query := "CREATE TABLE " + bad + "(extra BIGINT DEFAULT nextval('rp_inherit_draw')) " + c.suffix
						s := referenceRangeError(query, c.code, c.message, referenceDiagnostic{})
						s.prepared = route.prepared
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
						unchanged(t)
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='"+bad+"' ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
						for _, typ := range []string{bad, "_" + bad} {
							referenceRunStep(t, p.ctx, b.db, b.recorder, referenceRangeError("SELECT NULL::\""+typ+"\"", "42704", `type "`+typ+`" does not exist`, referenceDiagnostic{Position: "14"}))
						}
						// Successful reuse independently proves no half-created
						// child or row type blocks its exact destination name.
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE "+bad+"(probe INT)", 0))
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceRangeRows("SELECT probe AS k FROM "+bad))
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DROP TABLE "+bad, 0))
					}) {
						return
					}
				}
				// Incoming ordinary INHERITS stays admitted. Its physical child
				// has no copied parent rows and uses the existing inherited default.
				run(referenceExec("CREATE TABLE rp_inherit_valid(extra BIGINT DEFAULT 77) INHERITS (rp_inherit_plain)", 0))
				run(referenceSelect("SELECT k,payload,extra FROM rp_inherit_valid", []string{"k", "payload", "extra"}, []string{"INT4", "TEXT", "INT8"}, nil))
				run(referencePrepared(referenceExec("INSERT INTO rp_inherit_valid(k) VALUES($1)", 1, int64(7))))
				run(referencePrepared(referenceSelect("SELECT k,payload,extra FROM rp_inherit_valid ORDER BY k", []string{"k", "payload", "extra"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "plain-default", int64(77)}})))
				run(referenceExec("DROP TABLE rp_inherit_valid", 0))
				unchanged(t)
			})
		})
	}
}
