package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatAllIndexesAccessOwnership(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, step := range []fixture.Step{fixture.Exec("CREATE EXTENSION bloom", 0), fixture.Exec("CREATE TABLE stat_ownership(n integer,payload text)", 0), fixture.Exec("INSERT INTO stat_ownership VALUES(2,'a'),(2,'b'),(3,'z'),(NULL,NULL)", 4), fixture.Exec("CREATE INDEX stat_ownership_bloom ON stat_ownership USING bloom(n) WITH(length=1,col1=1)", 0)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		fixture.StatisticsCreateTables(t, p, b, c, "stat_ownership_marker")
		writer, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, writer, "SET enable_seqscan=off", 0)
		q := "SELECT payload FROM stat_ownership WHERE n=2"
		method := indexStatPlan(t, p, writer, q)
		if !b.PG && !strings.Contains(method, "Access Method: Bloom Candidates") {
			t.Fatal("native logical bulk bloom not chosen", method)
		}
		indexStatPayloads(t, p, b, writer, q, []string{"a", "b"})
		fixture.StatisticsExec(t, p, b, writer, "INSERT INTO stat_ownership_marker VALUES(1)", 1)
		retire()
		fixture.StatisticsAwaitTableCount(t, p, b, "stat_ownership_marker", "n_tup_ins", 1)
		// Entry count can include genuine lossy candidates; it must not be replaced
		// by output cardinality, and per-index heap fetches remain zero.
		observer := fixture.IdentityConnection(t, p.Ctx, b.DB)
		fixture.StatisticsExec(t, p, b, observer, "SET stats_fetch_consistency=none", 0)
		var r indexStatRow
		if !b.PG || strings.Contains(method, "Bitmap Index Scan") {
			r = indexStatAwaitPositive(t, p, observer, "stat_ownership_bloom")
			if r.Scans != 1 || r.Entries < 2 || r.Fetches != 0 {
				t.Fatal("bulk candidate ownership", r)
			}
		} else {
			delta := indexStatDelta(t, b, method, 1, 2)
			r = indexStatAwait(t, p, b, "stat_ownership_bloom", delta[0], delta[1], delta[2])
		}
		if got := userIndexShared(t, p, b, c, r.Name); !got.equal(r) {
			t.Fatal("bulk USER ownership differs from ALL", got, r)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE stat_indexonly(n integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO stat_indexonly VALUES(2),(2),(3)", 3)
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX stat_indexonly_btree ON stat_indexonly(n)", 0)
		if !b.PG {
			treeWriter, retireTree := fixture.StatisticsReportingConn(t, p, b)
			indexStatPrefer(t, p, b, treeWriter)
			if method := indexStatPlan(t, p, treeWriter, "SELECT n FROM stat_indexonly WHERE n=2"); !strings.Contains(method, "Access Method: Full Scan") {
				t.Fatal("B-tree presence invented native access", method)
			}
			fixture.RunStep(t, p.Ctx, treeWriter, b.Recorder, fixture.Select("SELECT n FROM stat_indexonly WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
			fixture.StatisticsExec(t, p, b, treeWriter, "INSERT INTO stat_ownership_marker VALUES(1)", 1)
			retireTree()
			fixture.StatisticsAwaitTableCount(t, p, b, "stat_ownership_marker", "n_tup_ins", 2)
			indexStatWant(t, indexStatGet(t, p, c, "stat_indexonly_btree"), 0, 0, 0)
			indexStatWant(t, userIndexShared(t, p, b, c, "stat_indexonly_btree"), 0, 0, 0)
			return
		}
		fixture.StatisticsExec(t, p, b, c, "VACUUM stat_indexonly", 0)
		only, retireOnly := fixture.StatisticsReportingConn(t, p, b)
		for _, q := range []string{"SET enable_seqscan=off", "SET enable_bitmapscan=off", "SET enable_indexonlyscan=on"} {
			fixture.StatisticsExec(t, p, b, only, q, 0)
		}
		// ANALYZE is the sole accounted execution; do not then execute the query a
		// second time and demand one search. Inspect actual heap fallback ownership.
		rows, err := only.QueryContext(p.Ctx, "EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) SELECT n FROM stat_indexonly WHERE n=2")
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		heap := -1
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
			if _, after, ok := strings.Cut(strings.TrimSpace(line), "Heap Fetches: "); ok {
				heap, err = strconv.Atoi(after)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		method = strings.Join(lines, "\n")
		delta := [3]int64{}
		if strings.Contains(method, "Index Only Scan using stat_indexonly_btree") {
			if heap < 0 {
				t.Fatal("actual index-only heap fallback not observed", method)
			}
			delta = [3]int64{1, 2, int64(heap)}
		} else {
			delta = indexStatDelta(t, b, method, 1, 2)
		}
		fixture.StatisticsExec(t, p, b, only, "INSERT INTO stat_ownership_marker VALUES(1)", 1)
		retireOnly()
		fixture.StatisticsAwaitTableCount(t, p, b, "stat_ownership_marker", "n_tup_ins", 2)
		r = indexStatAwait(t, p, b, "stat_indexonly_btree", delta[0], delta[1], delta[2])
		if got := userIndexShared(t, p, b, c, r.Name); !got.equal(r) {
			t.Fatal("actual heap fallback USER ownership differs from ALL", got, r)
		}
	})
}

func TestReferencePGStatAllIndexesReadOnly(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexStatCreate(t, p, b, c, "stat_readonly", "stat_readonly_hash")
		before := indexStatGet(t, p, c, "stat_readonly_hash")
		held, err := c.PrepareContext(p.Ctx, indexStatSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, tc := range []struct{ query, op string }{
			{"INSERT INTO pg_catalog.pg_stat_all_indexes(relname) VALUES('x')", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_all_indexes(relname) SELECT 'x' WHERE FALSE", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_all_indexes SET idx_scan=9 WHERE indexrelname='stat_readonly_hash'", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_all_indexes SET idx_scan=9 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_all_indexes WHERE indexrelname='stat_readonly_hash'", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_all_indexes WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_all_indexes", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				want := indexStatMutationDiagnostic(tc.op)
				if tc.op == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_all_indexes" is not a table`}
				}
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if prepared && tc.op != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				rows, err := held.QueryContext(p.Ctx, "stat_readonly_hash")
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal("mutation removed index", rows.Err())
				}
				rows.Close()
				if got := indexStatGet(t, p, c, "stat_readonly_hash"); !got.equal(before) {
					t.Fatal("mutation altered identity/counters", before, got)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM stat_readonly ORDER BY n,payload", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "a"}, {int64(2), "b"}, {int64(3), "z"}, {nil, nil}}))
			}
		}
		pool := p.Native
		if b.PG {
			pool = p.CopyPostgres
		}
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer copyConn.Close()
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_all_indexes(relname) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_all_indexes"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY published count/tag", result)
		}
		if got := indexStatGet(t, p, c, "stat_readonly_hash"); !got.equal(before) {
			t.Fatal("COPY changed target", before, got)
		}
	})
}

func TestReferencePGStatAllIndexesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"index_stat_reader"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE index_stat_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA index_stat_private", 0)
		indexStatCreate(t, p, b, c, "index_stat_private.stat_private", "stat_private_hash")
		pool := fixture.RoleAttributesPool(t, p, b, "index_stat_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT payload FROM index_stat_private.stat_private", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema index_stat_private", Position: "21"}))
		stmt, err := reader.PrepareContext(p.Ctx, indexStatSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		read := func() indexStatRow {
			t.Helper()
			var r indexStatRow
			err := stmt.QueryRowContext(p.Ctx, "stat_private_hash").Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		initial := read()
		indexStatWant(t, initial, 0, 0, 0)
		if initial.Schema != "index_stat_private" || initial.Table != "stat_private" || initial.Name != "stat_private_hash" || initial.Index <= 0 || initial.Parent <= 0 {
			t.Fatal("private identity redaction", initial)
		}
		writer, retire := fixture.StatisticsReportingConn(t, p, b)
		indexStatPrefer(t, p, b, writer)
		q := "SELECT payload FROM index_stat_private.stat_private WHERE n=2"
		if !indexStatSimple(t, b, indexStatPlan(t, p, writer, q)) {
			t.Fatal("private positive witness did not choose simple scan")
		}
		indexStatPayloads(t, p, b, writer, q, []string{"a", "b"})
		retire()
		indexStatAwait(t, p, b, "stat_private_hash", 1, 2, 2)
		fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency=none", 0)
		positive := read()
		indexStatWant(t, positive, 1, 2, 2)
		if positive.Index != initial.Index || positive.Parent != initial.Parent {
			t.Fatal("privilege identity changed")
		}
		if b.PG {
			const falseSQL = "SELECT * FROM pg_catalog.pg_stat_all_indexes WHERE FALSE"
			falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer falseStmt.Close()
			falseHeld := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
			fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, fixture.Select(falseSQL, indexStatNames, indexStatTypes, nil))
			aclOwner := fixture.IdentityConnection(t, p.Ctx, b.DB)
			defer aclOwner.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_stat_all_indexes TO PUBLIC")
			fixture.StatisticsExec(t, p, b, aclOwner, "REVOKE SELECT ON pg_catalog.pg_stat_all_indexes FROM PUBLIC", 0)
			_, err = stmt.QueryContext(p.Ctx, "stat_private_hash")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_all_indexes"})
			denied := fixture.ErrorDiagnostic(falseSQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_all_indexes"})
			denied.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, denied)
			fixture.StatisticsExec(t, p, b, aclOwner, "GRANT SELECT ON pg_catalog.pg_stat_all_indexes TO PUBLIC", 0)
			if r := read(); r.Index != initial.Index {
				t.Fatal("held ACL restore", r)
			}
			fixture.RunStep(t, p.Ctx, falseHeld, b.Recorder, fixture.Select(falseSQL, indexStatNames, indexStatTypes, nil))
		}
		// Effective identity can change after Prepare; this view never introduces
		// parent filtering even when the prepared user becomes the unprivileged role.
		ownerStmt, err := c.PrepareContext(p.Ctx, indexStatSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer ownerStmt.Close()
		fixture.StatisticsExec(t, p, b, c, "SET SESSION AUTHORIZATION index_stat_reader", 0)
		var id int64
		rows, err := ownerStmt.QueryContext(p.Ctx, "stat_private_hash")
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("role change hid parent")
		}
		raw := make([]any, 9)
		dest := make([]any, 9)
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if _, err := fmt.Sscan(fmt.Sprint(fixture.Normalize(raw[1])), &id); err != nil || id != initial.Index {
			t.Fatal("held role-change identity", raw, err)
		}
		fixture.StatisticsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", 0)
	})
}
