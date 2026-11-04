package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatAllTablesIdentityAndMetadata(t *testing.T) {
	tableStatIdentityAndMetadata(t, tableStatFamily{})
}

func tableStatIdentityAndMetadata(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f.create(t, p, b, c, "sat_identity")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		r := f.get(t, p, c, "sat_identity")
		schema := "public"
		if f.system {
			schema = "information_schema"
		}
		if r.ID <= 0 || r.Schema != schema || r.Name != "sat_identity" || r.Seq != 0 || r.Read != 0 || r.LastSeq.Valid || r.Index.Valid || r.Fetch.Valid || r.LastIndex.Valid || r.Inserts != 0 || r.Live != 0 || r.Dead != 0 || r.LastVacuum.Valid || r.LastAnalyze.Valid {
			t.Fatal("fresh exact ALL row", r)
		}
		tableStatAutomatic(t, r)
		var id, namespace, rowType, array, element, owner int64
		var kind string
		err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.relnamespace,c.relkind,c.reltype,t.typarray,a.typelem,t.typrelid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog."+f.view()+"'::regclass").Scan(&id, &namespace, &kind, &rowType, &array, &element, &owner)
		if err != nil || id <= 0 || namespace != 11 || kind != "v" || rowType <= 0 || array <= 0 || element != rowType || owner != id {
			t.Fatal("catalog type relationships", id, namespace, kind, rowType, array, element, owner, err)
		}
		wantID, wantRow, wantArray := int64(12146), int64(12148), int64(12147)
		if f.system {
			wantID, wantRow, wantArray = 12156, 12158, 12157
		}
		if f.user {
			wantID, wantRow, wantArray = 12165, 12167, 12166
		}
		if !b.PG && (id != wantID || rowType != wantRow || array != wantArray) {
			t.Fatal("native inventory identity", id, rowType, array)
		}
		var regType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_catalog."+f.view()+"'::regtype::oid").Scan(&regType); err != nil || regType != rowType {
			t.Fatal("regtype row identity", regType, err)
		}
		var attributes int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_attribute WHERE attrelid='pg_catalog."+f.view()+"'::regclass AND attnum>0 AND NOT attisdropped").Scan(&attributes); err != nil || attributes != 30 {
			t.Fatal("attribute inventory", attributes, err)
		}
		var definition, information string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('pg_catalog."+f.view()+"'::regclass)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT view_definition FROM information_schema.views WHERE table_schema='pg_catalog' AND table_name='"+f.view()+"'").Scan(&information); err != nil {
			t.Fatal(err)
		}
		terms := []string{"sum(pg_stat_get_numscans(i.indexrelid))", "max(pg_stat_get_lastscan(i.indexrelid))", "GROUP BY c.oid, n.nspname, c.relname"}
		if f.system {
			terms = []string{"FROM pg_stat_all_tables", "schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])", "schemaname ~ '^pg_toast'::text"}
		}
		if f.user {
			terms = []string{"FROM pg_stat_all_tables", "schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])", "schemaname !~ '^pg_toast'::text"}
		}
		for _, term := range terms {
			if !strings.Contains(definition, term) || !strings.Contains(information, term) {
				t.Fatal("released definition relationship", term, definition, information)
			}
		}
		tableStatAttributes(t, p, c, f.view())
		for _, qualified := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT * FROM " + f.view() + " WHERE relname=$1"
					if qualified {
						q = f.query()
					}
					if empty {
						q += " AND FALSE"
					}
					var rows *sql.Rows
					var err error
					var stmt *sql.Stmt
					if prepared {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err == nil {
							rows, err = stmt.QueryContext(p.Ctx, "sat_identity")
						}
					} else {
						rows, err = c.QueryContext(p.Ctx, q, "sat_identity")
					}
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					tableStatMetadata(t, columns, b.PG)
					count := 0
					for rows.Next() {
						raw := make([]any, 30)
						dest := make([]any, 30)
						for i := range raw {
							dest[i] = &raw[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, v := range raw {
							if i == 6 || i == 8 || tableStatTypes[i] == "TIMESTAMPTZ" {
								if v != nil {
									t.Fatal("fresh absent activity not NULL", i, v)
								}
								continue
							}
							switch tableStatTypes[i] {
							case "OID", "NAME":
								if b.PG {
									if _, ok := v.([]byte); !ok {
										t.Fatalf("pq raw %s=%T", tableStatNames[i], v)
									}
								} else if i == 0 {
									if _, ok := v.(int64); !ok {
										t.Fatalf("native OID=%T", v)
									}
								} else {
									if _, ok := v.(string); !ok {
										t.Fatalf("native NAME=%T", v)
									}
								}
							case "FLOAT8":
								if v != float64(0) {
									t.Fatal("typed zero elapsed", i, v)
								}
							default:
								if v != int64(0) {
									t.Fatal("typed zero counter", i, v)
								}
							}
						}
						count++
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					tableStatMetadata(t, columns, b.PG)
					if stmt != nil {
						stmt.Close()
					}
					want := 1
					if empty {
						want = 0
					}
					if count != want {
						t.Fatal("typed cursor cardinality", count, want)
					}
				}
			}
		}
		held, err := c.QueryContext(p.Ctx, f.query(), "sat_identity")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldColumns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		f.seed(t, p, b, "sat_identity")
		if !held.Next() {
			t.Fatal("retained row missing", held.Err())
		}
		var retained tableStatRow
		if err := held.Scan(retained.destinations()...); err != nil {
			t.Fatal(err)
		}
		held.Close()
		if retained.Inserts != 0 || retained.Seq != 0 || retained.LastSeq.Valid {
			t.Fatal("retained cursor recomputed after publication", retained)
		}
		tableStatMetadata(t, heldColumns, b.PG)
		if got := f.get(t, p, c, "sat_identity"); got.Inserts != 3 {
			t.Fatal("fresh row did not see publication", got)
		}
		if r.Inserts != 0 || r.LastSeq.Valid {
			t.Fatal("retained row changed", r)
		}
	})
}

func TestReferencePGStatAllTablesMembershipAndPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sat_reader"})
	fixture.SchemaReferenceCleanup(t, "sat_private")
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := c.ExecContext(ctx, "DROP SERVER IF EXISTS sat_server CASCADE"); err != nil {
				t.Error("foreign control cleanup", err)
			}
			if _, err := c.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS sat_fdw"); err != nil {
				t.Error("foreign wrapper cleanup", err)
			}
		}()
		tableStatCreate(t, p, b, c, "sat_members")
		for _, q := range []string{"CREATE VIEW sat_view AS SELECT n FROM sat_members", "CREATE SEQUENCE sat_sequence", "CREATE INDEX sat_index ON sat_members(n)", "CREATE MATERIALIZED VIEW sat_mat AS SELECT n FROM sat_members", "CREATE TABLE sat_partition(n integer) PARTITION BY RANGE(n)", "CREATE TABLE sat_leaf PARTITION OF sat_partition FOR VALUES FROM(0) TO(10)", "CREATE FOREIGN DATA WRAPPER sat_fdw", "CREATE SERVER sat_server FOREIGN DATA WRAPPER sat_fdw", "CREATE FOREIGN TABLE sat_foreign(n integer) SERVER sat_server"} {
			fixture.StatisticsExec(t, p, b, c, q, 0)
		}
		for _, tc := range []struct {
			name    string
			present bool
		}{{"sat_members", true}, {"sat_mat", true}, {"sat_partition", true}, {"sat_leaf", true}, {"sat_view", false}, {"sat_sequence", false}, {"sat_index", false}, {"sat_foreign", false}} {
			var count int
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_all_tables WHERE relname=$1", tc.name).Scan(&count); err != nil || (count == 1) != tc.present {
				t.Fatal("exact r/t/m/p membership", tc, count, err)
			}
		}
		for _, tc := range []struct {
			view  string
			count int64
		}{{"pg_stat_all_tables", 1}, {"pg_stat_user_tables", 1}, {"pg_stat_sys_tables", 0}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+tc.view+" WHERE relname='sat_members'", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
		var toast int64
		if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid FROM pg_class WHERE oid='sat_members'::regclass").Scan(&toast); err != nil {
			t.Fatal(err)
		}
		if toast > 0 {
			var count int
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_all_tables WHERE relid=$1", toast).Scan(&count); err != nil || count != 1 {
				t.Fatal("actual TOAST membership", toast, count, err)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE sat_reader LOGIN PASSWORD 'role-attributes-fixture'", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA sat_private", 0)
		tableStatCreate(t, p, b, c, "sat_private.sat_secret")
		pool := fixture.RoleAttributesPool(t, p, b, "sat_reader")
		reader, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT payload FROM sat_private.sat_secret", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema sat_private", Position: "21"}))
		fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency=none", 0)
		stmt, err := reader.PrepareContext(p.Ctx, tableStatSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		read := func() tableStatRow {
			var r tableStatRow
			if err := stmt.QueryRowContext(p.Ctx, "sat_secret").Scan(r.destinations()...); err != nil {
				t.Fatal(err)
			}
			return r
		}
		initial := read()
		if initial.Schema != "sat_private" || initial.Name != "sat_secret" || initial.ID <= 0 || initial.Inserts != 0 {
			t.Fatal("private row redacted", initial)
		}
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO sat_private.sat_secret VALUES(1,NULL),(2,'b')", 2)
		retire()
		tableStatAwait(t, p, b, "sat_secret", "inaccessible inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
		if got := read(); got.Inserts != 2 || got.ID != initial.ID {
			t.Fatal("inaccessible fields masked", got)
		}
		falseSQL := "SELECT * FROM pg_catalog.pg_stat_all_tables WHERE FALSE"
		falseStmt, err := reader.PrepareContext(p.Ctx, falseSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer falseStmt.Close()
		owner := fixture.IdentityConnection(t, p.Ctx, b.DB)
		defer owner.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_stat_all_tables TO PUBLIC")
		fixture.StatisticsExec(t, p, b, owner, "REVOKE SELECT ON pg_catalog.pg_stat_all_tables FROM PUBLIC", 0)
		denial := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for view pg_stat_all_tables"}
		rows, err := stmt.QueryContext(p.Ctx, "sat_secret")
		if rows != nil {
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &denial)
		held := fixture.HeldStatement{Executor: reader, Statement: falseStmt, SQL: falseSQL}
		step := fixture.ErrorDiagnostic(falseSQL, denial)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic(falseSQL, denial)
			step.Prepared = prepared
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, step)
		}
		fixture.StatisticsExec(t, p, b, owner, "GRANT SELECT ON pg_catalog.pg_stat_all_tables TO PUBLIC", 0)
		if got := read(); got.ID != initial.ID || got.Inserts != 2 {
			t.Fatal("restored SELECT ACL", got)
		}
	})
}

func TestReferencePGStatAllTablesReadOnly(t *testing.T) { tableStatReadOnly(t, tableStatFamily{}) }

func tableStatReadOnly(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f.create(t, p, b, c, "sat_readonly")
		f.seed(t, p, b, "sat_readonly")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		before := f.get(t, p, c, "sat_readonly")
		for _, tc := range []struct{ q, op string }{{"INSERT INTO pg_stat_all_tables(relname) VALUES('x')", "INSERT"}, {"INSERT INTO pg_stat_all_tables(relname) SELECT 'x' WHERE FALSE", "INSERT"}, {"UPDATE pg_stat_all_tables SET seq_scan=9 WHERE relname='sat_readonly'", "UPDATE"}, {"UPDATE pg_stat_all_tables SET seq_scan=9 WHERE FALSE", "UPDATE"}, {"DELETE FROM pg_stat_all_tables WHERE relname='sat_readonly'", "DELETE"}, {"DELETE FROM pg_stat_all_tables WHERE FALSE", "DELETE"}, {"TRUNCATE pg_stat_all_tables", "TRUNCATE"}} {
			for _, prepared := range []bool{false, true} {
				want := tableStatDiagnostic(tc.op)
				if tc.op == "TRUNCATE" {
					want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"` + f.view() + `" is not a table`}
				}
				step := fixture.ErrorDiagnostic(strings.ReplaceAll(tc.q, "pg_stat_all_tables", f.view()), want)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if prepared && tc.op != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if got := f.get(t, p, c, "sat_readonly"); !got.equal(before) {
					t.Fatal("rejected mutation changed monitoring", before, got)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM sat_readonly ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
		pool := p.Native
		if b.PG {
			pool = p.CopyPostgres
		}
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer copyConn.Close()
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog."+f.view()+"(relname) FROM STDIN", strings.NewReader("x\n"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "` + f.view() + `"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."})
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("rejected COPY count/tag", result)
		}
		if got := f.get(t, p, c, "sat_readonly"); !got.equal(before) {
			t.Fatal("COPY changed ALL row", got)
		}
	})
}
