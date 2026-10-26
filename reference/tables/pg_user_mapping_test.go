package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var pgUserMappingColumns = []string{"oid", "umuser", "umserver", "umoptions"}
var pgUserMappingTypes = []string{"OID", "OID", "OID", "_TEXT"}

func pgUserMappingAttributes() fixture.Step {
	return fixture.Select(
		"SELECT attnum AS num,attname AS name,atttypid AS type_oid,attnotnull,atttypmod AS typmod,attcollation AS collation,attndims AS dimensions FROM pg_catalog.pg_attribute WHERE attrelid=1418 AND attnum>0 ORDER BY attnum",
		[]string{"num", "name", "type_oid", "attnotnull", "typmod", "collation", "dimensions"},
		[]string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"},
		[][]any{
			{int64(1), "oid", int64(26), true, int64(-1), int64(0), int64(0)},
			{int64(2), "umuser", int64(26), true, int64(-1), int64(0), int64(0)},
			{int64(3), "umserver", int64(26), true, int64(-1), int64(0), int64(0)},
			{int64(4), "umoptions", int64(1009), false, int64(-1), int64(950), int64(1)},
		},
	)
}

func pgUserMappingMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, pgUserMappingColumns) {
		t.Fatal("pg_user_mapping columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(pgUserMappingColumns) {
		t.Fatal("pg_user_mapping metadata", meta, err)
	}
	for i, m := range meta {
		scan := reflect.TypeFor[int64]()
		if i == 3 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if m.Name() != pgUserMappingColumns[i] || m.DatabaseTypeName() != pgUserMappingTypes[i] || m.ScanType() != scan {
			t.Errorf("column %d name/type/scan=%s/%s/%v want %s/%s/%v", i, m.Name(), m.DatabaseTypeName(), m.ScanType(), pgUserMappingColumns[i], pgUserMappingTypes[i], scan)
		}
		nullable, known := m.Nullable()
		if nullable != (!pg && i == 3) || known != !pg {
			t.Errorf("column %d nullable=%t/%t want %t/%t", i, nullable, known, !pg && i == 3, !pg)
		}
	}
}

func TestReferencePGUserMappingShape(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		for _, q := range []string{"SELECT * FROM pg_user_mapping WHERE FALSE", "SELECT * FROM pg_catalog.pg_user_mapping WHERE FALSE"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, pgUserMappingColumns, pgUserMappingTypes, [][]any{})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMappingMetadata(t, rows, b.PG) }
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
		}
		s := pgUserMappingOIDStep(pgUserMappingAttributes(), b.PG)
		run(s)
		run(fixture.Prepared(s))
	})
}

const pgUserMappingPublicOptions = `{secret=shared,"label=a,b=c"}`
const pgUserMappingReaderOptions = `{secret=,"note=quote'back\\slash"}`

func pgUserMappingSetup(run func(fixture.Step)) {
	fdwMetadata(run)
	for _, role := range []string{"fdw_reader", "fdw_other", "fdw_owner"} {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader,fdw_other,fdw_owner", 0))
	run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(secret 'shared',label 'a,b=c')", 0))
	// The note element is independently prescribed as note=quote'back\slash.
	run(fixture.Exec(`CREATE USER MAPPING FOR fdw_reader SERVER fdw_server OPTIONS(secret '',note 'quote''back\slash')`, 0))
	run(fixture.Exec("CREATE USER MAPPING FOR fdw_other SERVER fdw_server", 0))
}

func pgUserMappingBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func pgUserMappingID(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, query, args...).Scan(&id); err != nil {
		t.Fatal("mapping identity", query, err)
	}
	if id <= 0 {
		t.Fatal("nonpositive object identity", query, id)
	}
	return id
}

type pgUserMappingExpected struct {
	server  string
	role    string // Empty denotes PUBLIC, whose role identity is zero.
	options any
}

// Capture only generated identities, through exact server/role relationships.
// Option content and every non-allocator expectation remain independently supplied.
func pgUserMappingRawRows(t *testing.T, p *fixture.Pair, c *sql.Conn, mappings []pgUserMappingExpected) [][]any {
	t.Helper()
	rows := make([][]any, 0, len(mappings))
	seen := make(map[int64]bool)
	for _, mapping := range mappings {
		server := pgUserMappingID(t, p, c, "SELECT oid FROM pg_foreign_server WHERE srvname=$1", mapping.server)
		var role int64
		if mapping.role != "" {
			role = pgUserMappingID(t, p, c, "SELECT oid FROM pg_roles WHERE rolname=$1", mapping.role)
		}
		id := pgUserMappingID(t, p, c, "SELECT oid FROM pg_user_mapping WHERE umserver=$1 AND umuser=$2", server, role)
		if seen[id] {
			t.Fatal("mapping identities are not distinct", id)
		}
		seen[id] = true
		rows = append(rows, []any{id, role, server, mapping.options})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0].(int64) < rows[j][0].(int64) })
	return rows
}

// pq exposes raw OIDs as decimal strings; native exposes int64. Preserve the
// numeric identity assertions and adapt only the expected wire representation.
func pgUserMappingOIDStep(s fixture.Step, pg bool) fixture.Step {
	if !pg {
		return s
	}
	want := make([][]any, len(s.Rows))
	for i, row := range s.Rows {
		want[i] = append([]any(nil), row...)
		for j, value := range row {
			if s.Types[j] == "OID" && value != nil {
				want[i][j] = strconv.FormatInt(value.(int64), 10)
			}
		}
	}
	s.Rows = want
	return s
}

func pgUserMappingRaw(query string, rows [][]any, pg bool) fixture.Step {
	s := fixture.Select(query, pgUserMappingColumns, pgUserMappingTypes, rows)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgUserMappingMetadata(t, rows, pg) }
	return pgUserMappingOIDStep(s, pg)
}

func pgUserMappingDenied(query string) fixture.Step {
	s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_user_mapping"))
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func TestReferencePGUserMappingMetadata(t *testing.T) {
	t.Run("Smoke", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
			before := reader.snapshot()
			pgUserMappingSetup(run)
			run(fixture.Exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_meta", 0))
			run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_second", 0))
			rows := pgUserMappingRawRows(t, p, c, []pgUserMappingExpected{
				{"fdw_server", "", pgUserMappingPublicOptions},
				{"fdw_server", "fdw_reader", pgUserMappingReaderOptions},
				{"fdw_server", "fdw_other", nil},
				{"fdw_second", "fdw_reader", nil},
			})
			pgUserMappingBoth(run, pgUserMappingRaw("SELECT * FROM pg_user_mapping ORDER BY oid", rows, b.PG))
			pgUserMappingBoth(run, pgUserMappingRaw("SELECT * FROM pg_catalog.pg_user_mapping ORDER BY oid", rows, b.PG))
			identity := fixture.Select(
				"SELECT s.srvname,r.rolname,m.umuser=0 AS public_mapping,m.umoptions FROM pg_user_mapping m JOIN pg_foreign_server s ON m.umserver=s.oid LEFT JOIN pg_roles r ON m.umuser=r.oid ORDER BY s.srvname,r.rolname",
				[]string{"srvname", "rolname", "public_mapping", "umoptions"}, []string{"NAME", "NAME", "BOOL", "_TEXT"},
				[][]any{
					{"fdw_second", "fdw_reader", false, nil},
					{"fdw_server", "fdw_other", false, nil},
					{"fdw_server", "fdw_reader", false, pgUserMappingReaderOptions},
					{"fdw_server", nil, true, pgUserMappingPublicOptions},
				})
			pgUserMappingBoth(run, identity)
			pgUserMappingBoth(run, fixture.Select(
				"SELECT r.rolname,m.umoptions[1] AS first,m.umoptions[2] AS second FROM pg_user_mapping m LEFT JOIN pg_roles r ON m.umuser=r.oid JOIN pg_foreign_server s ON m.umserver=s.oid WHERE s.srvname='fdw_server' ORDER BY r.rolname",
				[]string{"rolname", "first", "second"}, []string{"NAME", "TEXT", "TEXT"},
				[][]any{{"fdw_other", nil, nil}, {"fdw_reader", "secret=", `note=quote'back\slash`}, {nil, "secret=shared", "label=a,b=c"}}))
			pgUserMappingBoth(run, pgUserMappingOIDStep(pgUserMappingAttributes(), b.PG))
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			pgUserMappingBoth(run, pgUserMappingDenied("SELECT * FROM pg_user_mapping"))
			pgUserMappingBoth(run, fixture.Select(
				"SELECT usename,umoptions FROM pg_user_mappings WHERE srvname='fdw_server' ORDER BY usename",
				[]string{"usename", "umoptions"}, []string{"NAME", "_TEXT"},
				[][]any{{"fdw_other", nil}, {"fdw_reader", nil}, {"public", nil}}))
			run(fixture.Exec("RESET ROLE", 0))
			pgUserMappingBoth(run, pgUserMappingRaw("SELECT * FROM pg_user_mapping ORDER BY oid", rows, b.PG))
			if !b.PG && reader.snapshot() != before {
				t.Fatal("metadata inspection invoked foreign provider", before, reader.snapshot())
			}
		})
	})
}

func TestReferencePGUserMappingSecurity(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
		if b.PG {
			// Scoped to this scenario: catalog ACLs survive the public-schema reset.
			// The borrowed fixture connection remains owned by fdwScenario.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, query := range []string{
					"RESET SESSION AUTHORIZATION",
					"DROP OWNED BY fdw_reader CASCADE",
				} {
					if _, err := c.ExecContext(ctx, query); err != nil {
						t.Error("pg_user_mapping cleanup", err)
					}
				}
			})
		}
		before := reader.snapshot()
		pgUserMappingSetup(run)
		run(fixture.Exec("ALTER USER MAPPING FOR fdw_other SERVER fdw_server OPTIONS(ADD secret 'other')", 0))
		rows := pgUserMappingRawRows(t, p, c, []pgUserMappingExpected{
			{"fdw_server", "", pgUserMappingPublicOptions},
			{"fdw_server", "fdw_reader", pgUserMappingReaderOptions},
			{"fdw_server", "fdw_other", "{secret=other}"},
		})
		viewQuery := "SELECT umid,srvid,srvname,umuser,usename,umoptions FROM pg_user_mappings WHERE srvname='fdw_server' ORDER BY umid"
		viewStatement, err := c.PrepareContext(p.Ctx, viewQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer viewStatement.Close()
		viewHeld := fixture.HeldStatement{Executor: c, Statement: viewStatement, SQL: viewQuery}
		view := func(readerOptions, publicOptions, otherOptions any) {
			t.Helper()
			want := make([][]any, 0, len(rows))
			for _, row := range rows {
				principal, options := "public", publicOptions
				switch row[3] {
				case pgUserMappingReaderOptions:
					principal, options = "fdw_reader", readerOptions
				case "{secret=other}":
					principal, options = "fdw_other", otherOptions
				}
				want = append(want, []any{row[0], row[2], "fdw_server", row[1], principal, options})
			}
			s := fixture.Select(viewQuery, []string{"umid", "srvid", "srvname", "umuser", "usename", "umoptions"}, []string{"OID", "OID", "NAME", "OID", "NAME", "_TEXT"}, want)
			s = pgUserMappingOIDStep(s, b.PG)
			pgUserMappingBoth(run, s)
			fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, s)
		}
		view(pgUserMappingReaderOptions, pgUserMappingPublicOptions, "{secret=other}")
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		view(nil, nil, nil)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		view(pgUserMappingReaderOptions, nil, nil)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM fdw_reader", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_other", 0))
		run(fixture.Exec("GRANT fdw_other TO fdw_reader", 0))
		run(fixture.Exec("SET ROLE fdw_reader", 0))
		view(pgUserMappingReaderOptions, nil, nil)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO fdw_owner", 0))
		run(fixture.Exec("ALTER SERVER fdw_server OWNER TO fdw_owner", 0))
		run(fixture.Exec("SET ROLE fdw_owner", 0))
		view(nil, pgUserMappingPublicOptions, nil)
		run(fixture.Exec("RESET ROLE", 0))

		// Prepare both shapes before changing authority. Both must be denied
		// initially, including the empty query; native GRANT persists for the
		// remainder of this scenario because source REVOKE remains unsupported.
		type mappingProbe struct {
			step fixture.Step
			held fixture.HeldStatement
		}
		var probes []mappingProbe
		for _, query := range []string{"SELECT * FROM pg_user_mapping ORDER BY oid", "SELECT * FROM pg_user_mapping WHERE FALSE"} {
			want := rows
			if query == "SELECT * FROM pg_user_mapping WHERE FALSE" {
				want = [][]any{}
			}
			s := pgUserMappingRaw(query, want, b.PG)
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			probes = append(probes, mappingProbe{s, held})
			pgUserMappingBoth(run, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			denied := pgUserMappingDenied(query)
			pgUserMappingBoth(run, denied)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			run(fixture.Exec("RESET ROLE", 0))
		}
		if !b.PG {
			run(fixture.Select("SELECT relacl FROM pg_class WHERE oid=1418", []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{nil}}))
		}
		for _, prepared := range []bool{false, true} {
			grant := fixture.Exec("GRANT SELECT ON pg_catalog.pg_user_mapping TO fdw_reader", 0)
			grant.Prepared = prepared
			run(grant)
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			for _, probe := range probes {
				pgUserMappingBoth(run, probe.step)
				fixture.RunStep(t, p.Ctx, probe.held, b.Recorder, probe.step)
			}
			view(pgUserMappingReaderOptions, nil, nil)
			run(fixture.Exec("RESET ROLE", 0))
		}
		if b.PG {
			run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_user_mapping FROM fdw_reader", 0))
			run(fixture.Exec("SET ROLE fdw_reader", 0))
			for _, probe := range probes {
				denied := pgUserMappingDenied(probe.step.Query)
				fixture.RunStep(t, p.Ctx, probe.held, b.Recorder, denied)
			}
			run(fixture.Exec("RESET ROLE", 0))
		} else {
			// Native source REVOKE is still rejected, without undoing GRANT.
			acl := fixture.Select("SELECT has_table_privilege('fdw_reader','pg_catalog.pg_user_mapping','SELECT') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}})
			run(acl)
			for _, prepared := range []bool{false, true} {
				unsupported := fixture.ErrorDiagnostic("REVOKE SELECT ON pg_catalog.pg_user_mapping FROM fdw_reader", fixture.ControlsReferenceDiagnostic("0A000", "statement is not supported"))
				unsupported.Prepared, unsupported.ErrorStage = prepared, "execute"
				run(unsupported)
				run(acl)
				run(fixture.Exec("SET ROLE fdw_reader", 0))
				for _, probe := range probes {
					pgUserMappingBoth(run, probe.step)
					fixture.RunStep(t, p.Ctx, probe.held, b.Recorder, probe.step)
				}
				view(pgUserMappingReaderOptions, nil, nil)
				run(fixture.Exec("RESET ROLE", 0))
			}
		}
		if !b.PG && reader.snapshot() != before {
			t.Fatal("mapping security checks invoked foreign provider", before, reader.snapshot())
		}
	})
}
