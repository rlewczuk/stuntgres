//go:build reference

package reference_test

import "testing"

// Real PostgreSQL GRANT witnesses only. Native has no public ACL mutation API;
// the corresponding engine regressions invoke its actual SetObjectACL consumer.
func TestReferenceInsertSelectEmptyManifestColumnGrantOracle(t *testing.T) {
	const writer = "insert_select_empty_manifest_writer"
	for _, tc := range []struct {
		name, query, denied      string
		emptySource              bool
		sourceGrant, targetGrant string
		count                    int64
		values                   []any
	}{
		{name: "constant_column_select", query: "INSERT INTO t SELECT 7 FROM s", sourceGrant: "SELECT(i)", targetGrant: "INSERT", count: 3, values: []any{int64(7), int64(7), int64(7), int64(9)}},
		{name: "constant_empty_relation", query: "INSERT INTO t SELECT 7 FROM s", emptySource: true, sourceGrant: "SELECT(i)", targetGrant: "INSERT", values: []any{int64(9)}},
		{name: "constant_filtered", query: "INSERT INTO t SELECT 7 FROM s WHERE FALSE", sourceGrant: "SELECT(i)", targetGrant: "INSERT", values: []any{int64(9)}},
		{name: "zero_width_column_select", query: "INSERT INTO t SELECT FROM s", sourceGrant: "SELECT(i)", targetGrant: "INSERT", count: 3, values: []any{int64(9), nil, nil, nil}},
		{name: "zero_width_empty_relation", query: "INSERT INTO t SELECT FROM s", emptySource: true, sourceGrant: "SELECT(i)", targetGrant: "INSERT", values: []any{int64(9)}},
		{name: "zero_width_filtered", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceGrant: "SELECT(i)", targetGrant: "INSERT", values: []any{int64(9)}},
		{name: "empty_target_singleton", query: "INSERT INTO t (SELECT)", targetGrant: "INSERT(i)", count: 1, values: []any{int64(9), nil}},
		{name: "empty_target_source", query: "INSERT INTO t SELECT FROM s", sourceGrant: "SELECT", targetGrant: "INSERT(i)", count: 3, values: []any{int64(9), nil, nil, nil}},
		{name: "both_empty_column_manifests", query: "INSERT INTO t SELECT FROM s", sourceGrant: "SELECT(i)", targetGrant: "INSERT(i)", count: 3, values: []any{int64(9), nil, nil, nil}},
		{name: "empty_target_filtered", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceGrant: "SELECT(i)", targetGrant: "INSERT(i)", values: []any{int64(9)}},
		{name: "constant_no_select", query: "INSERT INTO t SELECT 7 FROM s", targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "empty_relation_no_select", query: "INSERT INTO t SELECT 7 FROM s", emptySource: true, targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "filtered_no_select", query: "INSERT INTO t SELECT 7 FROM s WHERE FALSE", targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "zero_width_no_select", query: "INSERT INTO t SELECT FROM s", targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "zero_width_filtered_no_select", query: "INSERT INTO t SELECT FROM s WHERE FALSE", targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "source_wrong_column_privilege", query: "INSERT INTO t SELECT 7 FROM s", sourceGrant: "INSERT(i)", targetGrant: "INSERT", denied: "s", values: []any{int64(9)}},
		{name: "empty_target_no_insert", query: "INSERT INTO t (SELECT)", denied: "t", values: []any{int64(9)}},
		{name: "empty_target_filtered_no_insert", query: "INSERT INTO t SELECT FROM s WHERE FALSE", sourceGrant: "SELECT(i)", denied: "t", values: []any{int64(9)}},
		{name: "target_wrong_column_privilege", query: "INSERT INTO t (SELECT)", targetGrant: "SELECT(i)", denied: "t", values: []any{int64(9)}},
	} {
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepare"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				roleReferenceCleanup(t, []string{writer})
				p := newReferencePair(t)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
				run(referenceExec("CREATE ROLE "+writer, 0))
				run(referenceExec("CREATE TABLE s(unused INT,i INT)", 0))
				run(referenceExec("CREATE TABLE t(unused INT,i INT)", 0))
				run(referenceExec("ALTER TABLE s DROP unused", 0))
				run(referenceExec("ALTER TABLE t DROP unused", 0))
				if !tc.emptySource {
					run(referenceExec("INSERT INTO s VALUES(1),(1),(2)", 3))
				}
				run(referenceExec("INSERT INTO t VALUES(9)", 1))
				grants := []string{"GRANT USAGE ON SCHEMA public TO " + writer}
				if tc.sourceGrant != "" {
					grants = append(grants, "GRANT "+tc.sourceGrant+" ON s TO "+writer)
				}
				if tc.targetGrant != "" {
					grants = append(grants, "GRANT "+tc.targetGrant+" ON t TO "+writer)
				}
				for _, q := range grants {
					if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				conn, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
				referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
				t.Run("consumer", func(t *testing.T) {
					s := referenceExec(tc.query, tc.count)
					if tc.denied != "" {
						s = referenceImplicitError(tc.query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + tc.denied})
						s.errorStage = "execute"
					}
					s.prepared = prepared
					referenceRunStep(t, p.ctx, conn, p.postgresRecord, s)
				})
				referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
				want := make([][]any, len(tc.values))
				for i, value := range tc.values {
					want[i] = []any{value}
				}
				referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
			})
		}
	}
	for _, table := range []string{"s", "t"} {
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepared"
			}
			t.Run("dropped_grant_"+table+"/"+mode, func(t *testing.T) {
				roleReferenceCleanup(t, []string{writer})
				p := newReferencePair(t)
				conn, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, p.postgresRecord, s) }
				run(referenceExec("CREATE ROLE "+writer, 0))
				run(referenceExec("CREATE TABLE s(i INT,j INT)", 0))
				run(referenceExec("CREATE TABLE t(i INT,j INT)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,2),(1,2)", 2))
				run(referenceExec("INSERT INTO t VALUES(9,9)", 1))
				grants := []string{"GRANT USAGE ON SCHEMA public TO " + writer}
				q := "INSERT INTO t SELECT 7 FROM s"
				want := [][]any{{int64(7)}, {int64(7)}, {int64(9)}}
				if table == "s" {
					grants = append(grants, "GRANT SELECT(j) ON s TO "+writer, "GRANT INSERT ON t TO "+writer)
				} else {
					grants = append(grants, "GRANT INSERT(j) ON t TO "+writer, "GRANT SELECT ON s TO "+writer)
					q = "INSERT INTO t SELECT FROM s"
					want = [][]any{{int64(9)}, {nil}, {nil}}
				}
				for _, grant := range grants {
					if _, err := conn.ExecContext(p.ctx, grant); err != nil {
						t.Fatal(err)
					}
				}
				run(referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
				success := referenceExec(q, 2)
				success.prepared = prepared
				run(success)
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceExec("ALTER TABLE "+table+" DROP j", 0))
				run(referenceExec("SET SESSION AUTHORIZATION "+writer, 0))
				failure := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
				failure.prepared, failure.errorStage = prepared, "execute"
				run(failure)
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, want))
			})
		}
	}
}
