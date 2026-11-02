package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func passwordCheckSentinel() fixture.Step {
	return fixture.Select("SELECT id,value FROM pc_control_sentinel ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
}

func passwordCheckProvider(t *testing.T, p *fixture.Pair, e *sql.Conn, r *fixture.Recorder, prepared bool) [2]int64 {
	t.Helper()
	var ids [2]int64
	if err := e.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='unaccent'").Scan(&ids[0]); err != nil { t.Fatal(err) }
	if err := e.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE proname='unaccent' AND pronargs=1").Scan(&ids[1]); err != nil { t.Fatal(err) }
	for _, id := range ids { if id <= 0 || id > 4294967295 { t.Fatal("valid provider identity", ids) } }
	run := passwordCheckRun(t, p, e, r, prepared)
	run(fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE oid=$1::oid", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"unaccent", "1.1", true}}, ids[0]))
	run(fixture.Select("SELECT count(*) AS members FROM pg_depend WHERE classid=1255 AND objid=$1::oid AND refclassid=3079 AND refobjid=$2::oid AND deptype='e'", []string{"members"}, []string{"INT8"}, [][]any{{int64(1)}}, ids[1], ids[0]))
	run(fixture.Select("SELECT unaccent('Hôtel') AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
	run(fixture.Select("SELECT count(*) AS modules FROM pg_extension WHERE extname='passwordcheck'", []string{"modules"}, []string{"INT8"}, [][]any{{int64(0)}}))
	return ids
}

func passwordCheckDestination(t *testing.T, p *fixture.Pair, dump string, configured bool) {
	t.Helper()
	d := stuntgres.NewDriver()
	if configured { if err := d.RegisterPasswordCheck(); err != nil { t.Fatal(err) } }
	connector, err := d.OpenConnector(fmt.Sprintf("passwordcheck-destination/%s/%v", t.Name(), configured))
	if err != nil { t.Fatal(err) }
	control := sql.OpenDB(connector)
	t.Cleanup(func() { if err := control.Close(); err != nil { t.Error(err) } })
	db, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil { t.Fatal("independent destination Restore", err) }
	c, err := db.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		run := passwordCheckRun(t, p, c, recorder, prepared)
		run(passwordCheckSentinel())
		passwordCheckProvider(t, p, c, recorder, prepared)
	}
	run := passwordCheckRun(t, p, c, recorder, false)
	query := "CREATE ROLE pc_destination PASSWORD 'x'"
	if configured {
		run(passwordCheckShow("8B"))
		run(passwordCheckShort(query, 8))
		run(fixture.RoleTransactionExists("pc_destination", false))
	} else {
		// A genuine independent unregistered Driver, not a disabled hook in the
		// source engine. Restoring SQL objects must not import startup activation.
		run(fixture.Exec(query, 0))
		run(fixture.RoleTransactionExists("pc_destination", true))
	}
}

func passwordCheckControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, prepared bool, role string) {
	t.Helper()
	run := passwordCheckRun(t, p, c, r, prepared)
	stored := passwordCheckSCRAM(t, "role-attributes-fixture")
	run(fixture.Exec("CREATE ROLE "+pq.QuoteIdentifier(role)+" LOGIN CONNECTION LIMIT 1 PASSWORD "+pq.QuoteLiteral(stored), 0))
	run(fixture.Exec("COMMENT ON ROLE "+pq.QuoteIdentifier(role)+" IS 'control role retained'", 0))
	run(fixture.Exec("CREATE TABLE pc_control_sentinel(id INT,value TEXT)", 0))
	run(fixture.Exec("INSERT INTO pc_control_sentinel VALUES(7,'kept')", 1))
	run(fixture.Exec("CREATE EXTENSION unaccent", 0))
	roleOID := passwordCheckOID(t, p, c, role)
	ids := passwordCheckProvider(t, p, c, r, prepared)
	run(fixture.Exec("SET passwordcheck.min_password_length=12", 0))
	checkRole := func() {
		if oid := passwordCheckOID(t, p, c, role); oid != roleOID { t.Fatal("controls changed shared role identity", roleOID, oid) }
		passwordCheckCredential(t, p, b, role, sql.NullString{String: stored, Valid: true})
		run(fixture.Select("SELECT shobj_description(oid,'pg_authid') AS comment FROM pg_roles WHERE rolname="+pq.QuoteLiteral(role), []string{"comment"}, []string{"TEXT"}, [][]any{{"control role retained"}}))
		run(passwordCheckShow("12B"))
		run(passwordCheckShort("ALTER ROLE "+pq.QuoteIdentifier(role)+" PASSWORD 'Abcdef12'", 12))
		physical, err := fixture.RoleAttributesPhysical(t, p, b, role)
		if err != nil { t.Fatal("controls lost role LOGIN or credential", err) }
		if err := physical.Close(); err != nil { t.Fatal(err) }
	}
	checkRole()
	run(passwordCheckSentinel())
	if b.PG { return } // Reset/Dump/Restore are explicitly native public APIs.
	retained, err := c.QueryContext(p.Ctx, passwordCheckSentinel().Query)
	if err != nil { t.Fatal(err) }
	defer retained.Close()
	columns, err := retained.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "value"}) { t.Fatal(columns, err) }
	meta, err := retained.ColumnTypes()
	if err != nil || len(meta) != 2 || meta[0].DatabaseTypeName() != "INT4" || meta[1].DatabaseTypeName() != "TEXT" { t.Fatal("retained metadata", meta, err) }
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	for _, forbidden := range []string{role, stored, "PASSWORD", "SCRAM", "passwordcheck", "shared_preload_libraries", "LOAD "} {
		if strings.Contains(dump, forbidden) { t.Fatal("dump serialized shared credential/startup resource", forbidden) }
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	checkRole()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
	checkRole()
	// Successful replacement allocates legitimate destination identities. It
	// does not promise source OIDs survive Reset/Restore.
	ids = passwordCheckProvider(t, p, c, r, prepared)
	for _, route := range []bool{false, true} { passwordCheckRun(t, p, c, r, route)(passwordCheckSentinel()) }
	for _, suffix := range []string{"CREATE SEQUENCE pc_bad_import CACHE 0;", "CREATE ROLE pc_forbidden_import PASSWORD 'Abcdef12';"} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+suffix))
		if err == nil { t.Fatal("invalid trailing Restore input accepted", suffix) }
		if strings.HasPrefix(suffix, "CREATE SEQUENCE") {
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
		}
		checkRole()
		run(passwordCheckSentinel())
		if after := passwordCheckProvider(t, p, c, r, prepared); after != ids { t.Fatal("failed Restore replaced provider/member identities", ids, after) }
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != dump { t.Fatal("failed Restore changed persistent SQL state") }
	}
	if after, err := retained.Columns(); err != nil || !reflect.DeepEqual(after, columns) { t.Fatal("retained column names changed", after, err) }
	if after, err := retained.ColumnTypes(); err != nil || len(after) != 2 || after[0].DatabaseTypeName() != "INT4" || after[1].DatabaseTypeName() != "TEXT" { t.Fatal("retained column types changed", after, err) }
	if !retained.Next() { t.Fatal("retained snapshot lost row", retained.Err()) }
	var id int64
	var value string
	if err := retained.Scan(&id, &value); err != nil || id != 7 || value != "kept" { t.Fatal("retained snapshot value", id, value, err) }
	if retained.Next() || retained.Err() != nil { t.Fatal("retained snapshot cardinality", retained.Err()) }
	if err := retained.Close(); err != nil { t.Fatal(err) }
	passwordCheckDestination(t, p, dump, false)
	passwordCheckDestination(t, p, dump, true)
}

func TestReferencePasswordCheckControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			passwordCheckScenario(t, []string{"pc_control_role"}, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				passwordCheckControls(t, p, b, c, r, prepared, "pc_control_role")
			})
		})
	}
}
