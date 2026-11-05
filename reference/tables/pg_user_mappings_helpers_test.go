package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var pgUserMappingsColumns = []string{"umid", "srvid", "srvname", "umuser", "usename", "umoptions"}
var pgUserMappingsTypes = []string{"OID", "OID", "NAME", "OID", "NAME", "_TEXT"}
var pgUserMappingsRoles = []string{"umv_alice", "umv_bob", "umv_empty_user", "umv_owner", "umv_owner_member", "umv_owner_set", "umv_usage_holder", "umv_usage_member", "umv_alice_member", "umv_denied"}

const pgUserMappingsQuery = "SELECT * FROM pg_catalog.pg_user_mappings WHERE srvname=$1 ORDER BY usename"
const pgUserMappingsAlice = "{password=alice-secret,empty=,tag=é=🙂}"
const pgUserMappingsBob = "{password=bob-secret}"
const pgUserMappingsPublic = "{password=public-secret}"
const pgUserMappingsOwner = "{password=owner-secret}"

// These helpers extend fdwScenario; they do not own another scenario or backend loop.
func pgUserMappingsSetup(t *testing.T, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	// fdwScenario owns wrapper teardown. Restore the original session before it runs,
	// including on a failed assertion inside a restricted session or transaction.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Error("mapping session cleanup", err)
			}
		}
	})
	fdwMetadata(run)
	for _, role := range pgUserMappingsRoles {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO umv_owner", 0))
	run(fixture.Exec("ALTER SERVER fdw_server OWNER TO umv_owner", 0))
	run(fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_meta FROM umv_owner", 0))
}

func pgUserMappingsSeed(run func(fixture.Step), owner bool) {
	for _, q := range []string{
		"CREATE USER MAPPING FOR umv_alice SERVER fdw_server OPTIONS(password 'alice-secret',empty '',tag 'é=🙂')",
		"CREATE USER MAPPING FOR umv_bob SERVER fdw_server OPTIONS(password 'bob-secret')",
		"CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(password 'public-secret')",
		"CREATE USER MAPPING FOR umv_empty_user SERVER fdw_server",
	} {
		run(fixture.Exec(q, 0))
	}
	if owner {
		run(fixture.Exec("CREATE USER MAPPING FOR umv_owner SERVER fdw_server OPTIONS(password 'owner-secret')", 0))
	}
}

func pgUserMappingsSpecs(owner bool) []pgUserMappingExpected {
	m := []pgUserMappingExpected{{"fdw_server", "umv_alice", pgUserMappingsAlice}, {"fdw_server", "umv_bob", pgUserMappingsBob}, {"fdw_server", "", pgUserMappingsPublic}, {"fdw_server", "umv_empty_user", nil}}
	if owner {
		m = append(m, pgUserMappingExpected{"fdw_server", "umv_owner", pgUserMappingsOwner})
	}
	return m
}

// Only allocator identities come from privileged catalog queries. Names/options
// are supplied independently; a secret is never used to recognize a principal.
func pgUserMappingsRows(t *testing.T, p *fixture.Pair, c *sql.Conn, specs []pgUserMappingExpected) [][]any {
	t.Helper()
	raw := pgUserMappingRawRows(t, p, c, specs)
	byID := make(map[int64]pgUserMappingExpected, len(specs))
	for _, spec := range specs {
		server := pgUserMappingID(t, p, c, "SELECT oid FROM pg_foreign_server WHERE srvname=$1", spec.server)
		var role int64
		if spec.role != "" {
			role = pgUserMappingID(t, p, c, "SELECT oid FROM pg_roles WHERE rolname=$1", spec.role)
		}
		id := pgUserMappingID(t, p, c, "SELECT oid FROM pg_user_mapping WHERE umserver=$1 AND umuser=$2", server, role)
		byID[id] = spec
	}
	rows := make([][]any, 0, len(raw))
	for _, row := range raw {
		spec := byID[row[0].(int64)]
		name := spec.role
		if name == "" {
			name = "public"
		}
		rows = append(rows, []any{row[0], row[2], spec.server, row[1], name, spec.options})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i][2] != rows[j][2] {
			return rows[i][2].(string) < rows[j][2].(string)
		}
		return rows[i][4].(string) < rows[j][4].(string)
	})
	return rows
}

func pgUserMappingsMask(rows [][]any, allowed ...string) [][]any {
	out := make([][]any, len(rows))
	for i, row := range rows {
		out[i] = append([]any(nil), row...)
		out[i][5] = nil
		for _, name := range allowed {
			if name == "all" || name == row[4] {
				out[i][5] = row[5]
			}
		}
	}
	return out
}

func pgUserMappingsStep(query string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(query, pgUserMappingsColumns, pgUserMappingsTypes, rows, args...)
}

func pgUserMappingsRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, held *fixture.HeldStatement, rows [][]any, server string) {
	t.Helper()
	s := pgUserMappingsStep(pgUserMappingsQuery, rows, server)
	fixture.OidReferenceRows(t, p.Ctx, b, c, s)
	fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(s))
	if held != nil {
		fixture.OidReferenceRows(t, p.Ctx, b, *held, s)
	}
}

func pgUserMappingsHold(t *testing.T, p *fixture.Pair, c fixture.Executor) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, pgUserMappingsQuery)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: pgUserMappingsQuery}
}

func pgUserMappingsMetadata(t *testing.T, r *sql.Rows, pg bool) {
	t.Helper()
	meta, err := r.ColumnTypes()
	if err != nil || len(meta) != 6 {
		t.Fatal("view metadata width", meta, err)
	}
	for i, m := range meta {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		} else if pgUserMappingsTypes[i] == "OID" {
			scan = reflect.TypeFor[int64]()
		}
		if m.Name() != pgUserMappingsColumns[i] || m.DatabaseTypeName() != pgUserMappingsTypes[i] || m.ScanType() != scan {
			t.Fatal("view column metadata", i, m, scan)
		}
		if nullable, known := m.Nullable(); nullable != !pg || known != !pg {
			t.Errorf("view column %d nullability=%t/%t want %t/%t", i, nullable, known, !pg, !pg)
		}
		if n, known := m.Length(); n != 0 || known {
			t.Fatal("view column length", i, n, known)
		}
		if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("view column decimal size", i, precision, scale, known)
		}
	}
}

func pgUserMappingsConsume(t *testing.T, b fixture.Backend, r *sql.Rows, want [][]any) {
	t.Helper()
	defer r.Close()
	got := make([][]any, 0)
	for r.Next() {
		row := make([]any, 6)
		dest := make([]any, 6)
		for i := range row {
			dest[i] = &row[i]
		}
		if err := r.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = fixture.OidReferenceValue(t, b.PG, pgUserMappingsTypes[i], row[i])
		}
		got = append(got, row)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, want, false); !ok {
		t.Fatal("captured mapping rows", got, "want", want)
	}
}

func pgUserMappingsMissingColumn(run func(fixture.Step)) {
	d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_user_mappings", d)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
	}
}
