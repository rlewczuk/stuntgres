package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var dpPrivilegeColumns = []string{"grantor", "grantee", "table_catalog", "table_schema", "table_name", "privilege_type", "is_grantable", "with_hierarchy"}
var dpPrivilegeTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR"}

const dpPrivilegeFields = "grantor,grantee,table_catalog,table_schema,table_name,privilege_type,is_grantable,with_hierarchy"
const dpPrivilegeOrder = " ORDER BY table_schema,table_name,grantor,grantee,privilege_type"

func dpEnvironment(t *testing.T, p *fixture.Pair, c *sql.Conn) (string, string) {
	t.Helper()
	var owner, database string
	if err := c.QueryRowContext(p.Ctx, "SELECT current_user,current_database()").Scan(&owner, &database); err != nil {
		t.Fatal(err)
	}
	if owner == "" || database == "" {
		t.Fatal("empty environment identity", owner, database)
	}
	return owner, database
}

// Golden inputs omit only the environment-dependent catalog. Capture it from
// current_database(), not from the projection whose correctness is being tested.
func dpProjection(t *testing.T, p *fixture.Pair, c *sql.Conn, run func(fixture.Step), where string, want [][]any) {
	t.Helper()
	_, database := dpEnvironment(t, p, c)
	rows := make([][]any, 0, len(want))
	for _, r := range want {
		if len(r) != 7 {
			t.Fatal("invalid independently specified ACL row", r)
		}
		row := append([]any{}, r[:2]...)
		row = append(row, database)
		row = append(row, r[2:]...)
		rows = append(rows, row)
	}
	run(fixture.Select("SELECT "+dpPrivilegeFields+" FROM information_schema.table_privileges WHERE "+where+dpPrivilegeOrder, dpPrivilegeColumns, dpPrivilegeTypes, rows))
}

func TestReferenceRoleDefaultPrivilegesExposure(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		owner, _ := dpEnvironment(t, p, c)
		dpTable(run, "dp_absent", 7)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT,INSERT,MAINTAIN ON TABLES TO dp_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC", 0))
		dpTable(run, "dp_exposed", 9)
		run(fixture.Exec("CREATE VIEW dp_exposed_view AS SELECT x FROM dp_exposed", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW dp_exposed_mv AS SELECT x FROM dp_exposed", 1))
		run(fixture.Exec("CREATE SEQUENCE dp_exposed_seq", 0))
		run(fixture.Exec("CREATE TYPE dp_exposed_composite AS(x INT)", 0))
		run(fixture.Exec("CREATE INDEX dp_exposed_idx ON dp_exposed(x)", 0))
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed' AND grantee <> '"+owner+"'", [][]any{
			{owner, "PUBLIC", "public", "dp_exposed", "SELECT", "NO", "YES"},
			{owner, "dp_reader", "public", "dp_exposed", "INSERT", "YES", "NO"},
			{owner, "dp_reader", "public", "dp_exposed", "SELECT", "YES", "YES"},
		})
		dpProjection(t, p, c, run, "table_schema='public' AND (table_name='dp_exposed_mv' OR table_name='dp_exposed_seq' OR table_name='dp_exposed_composite' OR table_name='dp_exposed_idx')", nil)
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed_view' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_exposed_view", "INSERT", "YES", "NO"}, {owner, "dp_reader", "public", "dp_exposed_view", "SELECT", "YES", "YES"}})
		dpActor(run, "dp_reader", func() { run(fixture.Exec("GRANT SELECT ON dp_exposed TO dp_other", 0)) })
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed' AND grantee='dp_other'", [][]any{{"dp_reader", "dp_other", "public", "dp_exposed", "SELECT", "NO", "YES"}})
		dpActor(run, "dp_other", func() { dpRead(run, "dp_exposed", 9) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT ON dp_exposed FROM dp_reader CASCADE", 0))
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed' AND grantee='dp_other'", nil)
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed' AND grantee='dp_reader' AND privilege_type='SELECT'", [][]any{{owner, "dp_reader", "public", "dp_exposed", "SELECT", "NO", "YES"}})
		run(fixture.Exec("GRANT SELECT ON dp_exposed TO dp_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("CREATE TABLE dp_exposure_partition(x INT) PARTITION BY RANGE(x)", 0))
		run(fixture.Exec("CREATE TABLE dp_exposure_child PARTITION OF dp_exposure_partition FOR VALUES FROM(0) TO(20)", 0))
		dpProjection(t, p, c, run, "table_schema='public' AND (table_name='dp_exposure_partition' OR table_name='dp_exposure_child') AND grantee='dp_reader'", [][]any{
			{owner, "dp_reader", "public", "dp_exposure_child", "INSERT", "YES", "NO"},
			{owner, "dp_reader", "public", "dp_exposure_child", "SELECT", "YES", "YES"},
			{owner, "dp_reader", "public", "dp_exposure_partition", "INSERT", "YES", "NO"},
			{owner, "dp_reader", "public", "dp_exposure_partition", "SELECT", "YES", "YES"},
		})
		ownerRows := [][]any{}
		for _, bit := range []string{"DELETE", "INSERT", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"} {
			hierarchy := "NO"
			if bit == "SELECT" {
				hierarchy = "YES"
			}
			ownerRows = append(ownerRows, []any{owner, owner, "public", "dp_absent", bit, "YES", hierarchy})
		}
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_absent'", ownerRows)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE ALL ON TABLES FROM dp_reader,PUBLIC", 0))
		run(fixture.Exec("CREATE TABLE dp_columns_only(x INT)", 0))
		run(fixture.Exec("GRANT SELECT(x) ON dp_columns_only TO dp_reader", 0))
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_columns_only' AND grantee='dp_reader'", nil)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE ALL ON TABLES FROM CURRENT_USER", 0))
		run(fixture.Exec("CREATE TABLE dp_empty(x INT)", 0))
		// REL_18_0 aclmerge(empty global, absent scoped) returns absent, so this
		// creation expands hardwired owner rights. Explicit object-empty is distinct.
		createdOwnerRows := [][]any{}
		for _, r := range ownerRows {
			row := append([]any{}, r...)
			row[3] = "dp_empty"
			createdOwnerRows = append(createdOwnerRows, row)
		}
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_empty'", createdOwnerRows)
		run(fixture.Exec("REVOKE ALL ON dp_empty FROM CURRENT_USER", 0))
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_empty'", nil)
		// A nonempty scoped ACL does not invoke that empty-merge fallback: the
		// persisted global empty contributes no owner ordinary rights.
		dpSchema(run, "dp_owner_ns", "dp_owner")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE ALL ON TABLES FROM dp_owner", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner IN SCHEMA dp_owner_ns GRANT SELECT ON TABLES TO dp_reader", 0))
		dpActor(run, "dp_owner", func() {
			run(fixture.Exec("CREATE TABLE dp_owner_ns.dp_empty_scoped(x INT)", 0))
			dpDenied(run, "INSERT INTO dp_owner_ns.dp_empty_scoped VALUES(7)", "table", "dp_empty_scoped")
			dpDenied(run, "SELECT x FROM dp_owner_ns.dp_empty_scoped", "table", "dp_empty_scoped")
		})
		run(fixture.Exec("INSERT INTO dp_owner_ns.dp_empty_scoped VALUES(9)", 1))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_owner_ns.dp_empty_scoped", 9) })
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_empty_scoped'", [][]any{{"dp_owner", "dp_reader", "dp_owner_ns", "dp_empty_scoped", "SELECT", "NO", "YES"}})
		// Stored directives changed, but neither absent nor materialized old ACL is recomputed.
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_absent'", ownerRows)
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_exposed' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_exposed", "INSERT", "YES", "NO"}, {owner, "dp_reader", "public", "dp_exposed", "SELECT", "YES", "YES"}})
	})
}

func TestReferenceRoleDefaultPrivilegesExposureVisibility(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT dp_reader TO dp_member WITH INHERIT TRUE,SET TRUE", 0))
		run(fixture.Exec("GRANT dp_reader TO dp_set WITH INHERIT FALSE,SET TRUE", 0))
		run(fixture.Exec("CREATE SCHEMA dp_owner_ns AUTHORIZATION dp_owner", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT INSERT ON TABLES TO PUBLIC", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_visible", 9) })
		publicRow := [][]any{{"dp_owner", "PUBLIC", "dp_owner_ns", "dp_visible", "INSERT", "NO", "NO"}}
		both := [][]any{{"dp_owner", "PUBLIC", "dp_owner_ns", "dp_visible", "INSERT", "NO", "NO"}, {"dp_owner", "dp_reader", "dp_owner_ns", "dp_visible", "SELECT", "NO", "YES"}}
		where := "table_schema='dp_owner_ns' AND table_name='dp_visible' AND grantee <> 'dp_owner'"
		for _, actor := range []string{"dp_reader", "dp_member"} {
			dpActor(run, actor, func() {
				// No USAGE on dp_owner_ns: exposure has no extra namespace/access filter.
				dpDenied(run, "SELECT x FROM dp_owner_ns.dp_visible", "schema", "dp_owner_ns", prepared)
				dpProjection(t, p, c, run, where, both)
			})
		}
		for _, actor := range []string{"dp_other", "dp_set"} {
			dpActor(run, actor, func() { dpProjection(t, p, c, run, where, publicRow) })
		}
		dpActor(run, "dp_set", func() {
			run(fixture.Exec("SET ROLE dp_reader", 0))
			dpProjection(t, p, c, run, where, both)
			run(fixture.Exec("RESET ROLE", 0))
		})
		run(fixture.Exec("GRANT dp_owner TO dp_other WITH INHERIT TRUE", 0))
		dpActor(run, "dp_other", func() { dpProjection(t, p, c, run, where, both) })
		// A grantee inheriting the owner has implicit grantability without stored option bits.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_other", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_implicit", 11) })
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_implicit' AND grantee='dp_other'", [][]any{{"dp_owner", "dp_other", "dp_owner_ns", "dp_implicit", "SELECT", "YES", "YES"}})
	})
}

func TestReferenceRoleDefaultPrivilegesMetadata(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		owner, database := dpEnvironment(t, p, c)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_metadata", 9)
		q := "SELECT * FROM information_schema.table_privileges WHERE table_schema='public' AND table_name='dp_metadata' AND grantee='dp_reader'" + dpPrivilegeOrder
		s := fixture.Select(q, dpPrivilegeColumns, dpPrivilegeTypes, [][]any{{owner, "dp_reader", database, "public", "dp_metadata", "SELECT", "NO", "YES"}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, b.PG, types, dpPrivilegeColumns, dpPrivilegeTypes, []bool{true, true, true, true, true, true, true, true})
			for i, typ := range types {
				length, known := typ.Length()
				wantLength, wantKnown := int64(0), false
				if i == 5 {
					// character_data is unbounded: pq preserves its typmod length quirk;
					// native uses its existing scalar VARCHAR carrier.
					wantLength, wantKnown = int64(9223372036854775807), true
					if b.PG {
						wantLength = -5
					}
				}
				// yes_or_no contributes VARCHAR(3) base typmod without implementing domains.
				if i >= 6 {
					wantLength, wantKnown = 3, true
				}
				if length != wantLength || known != wantKnown {
					t.Fatalf("column %d Length=%d/%t want %d/%t", i, length, known, wantLength, wantKnown)
				}
				precision, scale, decimalKnown := typ.DecimalSize()
				if precision != 0 || scale != 0 || decimalKnown {
					t.Fatalf("column %d DecimalSize=%d/%d/%t", i, precision, scale, decimalKnown)
				}
			}
		}
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			want := reflect.TypeFor[string]()
			if b.PG && column < 5 {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("column %d raw %T=%v want %v", column, value, value, want)
			}
			return true
		}
		run(s)
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_metadata' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_metadata", "SELECT", "NO", "YES"}})
		empty := s
		empty.Query = "SELECT * FROM information_schema.table_privileges WHERE table_schema='public' AND table_name='dp_not_created'"
		empty.Rows = nil
		run(empty)
	})
}
