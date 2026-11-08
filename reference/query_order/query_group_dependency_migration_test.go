package reference_test

import (
	_ "embed"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

//go:embed testdata/000246_provisioner_job_timings.up.sql
var groupDependencyCoderMigration string

func groupDependencyMigration(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	groupDependencyExec(t, p, b, "CREATE TABLE provisioner_jobs(id uuid PRIMARY KEY, job_status text, worker_id uuid, error text, error_code text, updated_at timestamptz, created_at timestamptz, started_at timestamptz, completed_at timestamptz, canceled_at timestamptz)", "CREATE TABLE workspace_builds(id uuid PRIMARY KEY, job_id uuid UNIQUE REFERENCES provisioner_jobs(id), workspace_id uuid)")
	// This asset contains precisely three SQL statements and no quoted semicolons.
	for _, statement := range strings.Split(groupDependencyCoderMigration, ";") {
		if strings.TrimSpace(statement) != "" {
			groupDependencyExec(t, p, b, statement)
		}
	}
	groupDependencyExec(t, p, b,
		"INSERT INTO provisioner_jobs VALUES('00000000-0000-0000-0000-000000000001','succeeded','00000000-0000-0000-0000-000000000002','','','2026-01-01 00:00:10+00','2026-01-01 00:00:00+00','2026-01-01 00:00:02+00','2026-01-01 00:00:10+00',NULL)",
		"INSERT INTO workspace_builds VALUES('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000004')",
		"INSERT INTO provisioner_job_timings VALUES('00000000-0000-0000-0000-000000000001','2026-01-01 00:00:02+00','2026-01-01 00:00:03+00','init','src','act','res'),('00000000-0000-0000-0000-000000000001','2026-01-01 00:00:03+00','2026-01-01 00:00:05+00','plan','src','act','res'),('00000000-0000-0000-0000-000000000001','2026-01-01 00:00:05+00','2026-01-01 00:00:08+00','apply','src','act','res')")
	expected := fixture.Select("SELECT * FROM provisioner_job_stats ORDER BY job_id",
		[]string{"job_id", "job_status", "workspace_id", "worker_id", "error", "error_code", "updated_at", "queued_secs", "completion_secs", "canceled_secs", "init_secs", "plan_secs", "graph_secs", "apply_secs"},
		[]string{"UUID", "TEXT", "UUID", "UUID", "TEXT", "TEXT", "TIMESTAMPTZ", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"},
		[][]any{{"00000000-0000-0000-0000-000000000001", "succeeded", "00000000-0000-0000-0000-000000000004", "00000000-0000-0000-0000-000000000002", "", "", time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC), "2.000000", "8.000000", "0", "1.000000", "2.000000", "0", "3.000000"}})
	groupDependencyCheck(t, p, b, expected)
	groupDependencyExec(t, p, b,
		"INSERT INTO provisioner_jobs VALUES('00000000-0000-0000-0000-000000000005','pending',NULL,'','',NULL,NULL,NULL,NULL,NULL)",
		"INSERT INTO workspace_builds VALUES('00000000-0000-0000-0000-000000000007','00000000-0000-0000-0000-000000000005','00000000-0000-0000-0000-000000000008')")
	expected.Rows = append(expected.Rows, []any{"00000000-0000-0000-0000-000000000005", "pending", "00000000-0000-0000-0000-000000000008", nil, "", "", nil, "0", "0", "0", "0", "0", "0", "0"})
	groupDependencyCheck(t, p, b, expected)
	groupDependencyExec(t, p, b, "INSERT INTO provisioner_job_timings VALUES('00000000-0000-0000-0000-000000000001','2026-01-01 00:00:06+00','2026-01-01 00:00:07+00','init','src','act','res')")
	// The disjoint [2,3] and [6,7] intervals span five seconds, not their sum of two.
	expected.Rows[0][10] = "5.000000"
	groupDependencyCheck(t, p, b, expected)
}

func TestReferenceQueryGroupDependencyCoderMigration(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { groupDependencyMigration(t, p, b) })
}
