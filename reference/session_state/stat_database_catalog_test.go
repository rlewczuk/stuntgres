package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseSchema(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT * FROM pg_catalog.pg_stat_database WHERE false", statDatabaseNames, statDatabaseTypes, nil)
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil || len(cols) != 30 {
					t.Fatal(cols, err)
				}
				for i, col := range cols {
					if col.Name() != statDatabaseNames[i] || col.DatabaseTypeName() != statDatabaseTypes[i] {
						t.Fatal(i, col)
					}
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.relkind::text AS kind,count(a.attname) AS attributes FROM pg_catalog.pg_class c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid WHERE c.oid='pg_catalog.pg_stat_database'::regclass AND a.attnum>0 GROUP BY c.relkind", []string{"kind", "attributes"}, []string{"TEXT", "INT8"}, [][]any{{"v", int64(30)}}))
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_database WHERE datname=current_database()")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var values [30]any
		dest := make([]any, 30)
		for i := range dest {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if values[0] == nil || values[1] == nil || values[2] == nil || values[18] == nil {
			t.Fatal("lost typed identity/count/time", values)
		}
		if _, ok := values[18].(float64); !ok {
			t.Fatalf("FLOAT8 raw %T", values[18])
		}
	})
}
func TestReferenceStatDatabaseSharedBackendWitness(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// REL_18_6 system_views.sql CASE supplies literal zero despite manual NULL.
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT datid,datname,numbackends FROM pg_catalog.pg_stat_database WHERE datid=0", []string{"datid", "datname", "numbackends"}, []string{"OID", "NAME", "INT4"}, [][]any{{int64(0), nil, int64(0)}})
			s.Prepared = prepared
			fixture.OidReferenceRows(t, p.Ctx, b, c, s)
		}
	})
}
func TestReferenceStatDatabaseInventory(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_db_denied"})
	statDatabaseScenario(t, "inventory", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, create func(string) fixture.Backend) {
		target := create("empty")
		query := "SELECT datid::bigint,datname::text FROM pg_catalog.pg_stat_database WHERE datid<>0 ORDER BY datid"
		read := func(q string) [][]any {
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var result [][]any
			for rows.Next() {
				var oid int64
				var name string
				if err := rows.Scan(&oid, &name); err != nil {
					t.Fatal(err)
				}
				result = append(result, []any{oid, name})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return result
		}
		if got, want := read(query), read("SELECT oid::bigint,datname::text FROM pg_catalog.pg_database ORDER BY oid"); !reflect.DeepEqual(got, want) {
			t.Fatal(got, want)
		}
		before := statDatabaseGet(t, p, c, target.Name)
		if before.Backends != 0 || before.Sessions != 0 {
			t.Fatal("empty database", before)
		}
		_, retire := statDatabaseConn(t, p, target)
		if got := statDatabaseGet(t, p, c, target.Name); got.Backends != 1 {
			t.Fatal("live admission", got)
		}
		retire()
		statDatabaseAwait(t, p, c, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == 1 })
		statsExec(t, p, b, c, "CREATE ROLE stat_db_denied LOGIN PASSWORD 'role-core-fixture'", 0)
		statsExec(t, p, b, c, "REVOKE CONNECT ON DATABASE "+target.Name+" FROM PUBLIC", 0)
		denied, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, target.Name, "stat_db_denied")
		fixture.State(t, denied.PingContext(p.Ctx), "42501")
		if got := statDatabaseGet(t, p, c, target.Name); got.Backends != 0 || got.Sessions != 1 {
			t.Fatal("failed admission became established client", got)
		}
	})
}
func TestReferenceStatDatabaseReadOnly(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ operation, query, verb, action string }{
			{"INSERT", "INSERT INTO pg_catalog.pg_stat_database(datid) VALUES(0)", "insert into", "inserting into"},
			{"UPDATE", "UPDATE pg_catalog.pg_stat_database SET datname='x' WHERE false", "update", "updating"},
			{"DELETE", "DELETE FROM pg_catalog.pg_stat_database WHERE false", "delete from", "deleting from"},
		} {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + tc.verb + " view \"pg_stat_database\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.action + " the view, provide an INSTEAD OF " + tc.operation + " trigger or an unconditional ON " + tc.operation + " DO INSTEAD rule."}
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, tc.query)
					err = e
					if stmt != nil {
						_, err = stmt.ExecContext(p.Ctx)
						stmt.Close()
					}
				} else {
					_, err = c.ExecContext(p.Ctx, tc.query)
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
		}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_database" is not a table`}
		_, err := c.ExecContext(p.Ctx, "TRUNCATE pg_catalog.pg_stat_database")
		fixture.AssertDiagnostic(t, err, &want)
	})
}
func TestReferenceStatDatabaseTechnical(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var checksums string
		if err := c.QueryRowContext(p.Ctx, "SHOW data_checksums").Scan(&checksums); err != nil {
			t.Fatal(err)
		}
		off := strings.EqualFold(checksums, "off") || strings.EqualFold(checksums, "false")
		query := "SELECT conflicts=0 AS primary,checksum_last_failure IS NULL AS no_failure,checksum_failures IS NULL AS no_checksums FROM pg_catalog.pg_stat_database WHERE datname=current_database()"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"primary", "no_failure", "no_checksums"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, off}}))
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT blks_read=0 AS memory_only,temp_files=0 AS no_spill,temp_bytes=0 AS no_bytes,stats_reset IS NULL AS no_reset FROM pg_catalog.pg_stat_database WHERE datname=current_database()", []string{"memory_only", "no_spill", "no_bytes", "no_reset"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		}
	})
}
func TestReferenceStatDatabasePrivileges(t *testing.T) {
	names := []string{"stat_db_reader"}
	fixture.RoleReferenceCleanup(t, names)
	statDatabaseScenario(t, "privilege", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, create func(string) fixture.Backend) {
		target := create("target")
		base := statDatabaseSetup(t, p, target, c)
		statsExec(t, p, b, c, "CREATE ROLE stat_db_reader LOGIN PASSWORD 'role-core-fixture'", 0)
		statsExec(t, p, b, c, "SET ROLE stat_db_reader", 0)
		got := statDatabaseGet(t, p, c, target.Name)
		if got != base {
			t.Fatal("database aggregates were masked", base, got)
		}
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_database FROM PUBLIC", 0)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := b.DB.ExecContext(ctx, "RESET ROLE; GRANT SELECT ON pg_catalog.pg_stat_database TO PUBLIC"); err != nil {
				t.Error(err)
			}
		})
		statsExec(t, p, b, c, "SET ROLE stat_db_reader", 0)
		_, err := c.QueryContext(p.Ctx, "SELECT datid FROM pg_catalog.pg_stat_database")
		fixture.State(t, err, "42501")
		statsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
