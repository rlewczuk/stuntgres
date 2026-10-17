package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCatalogRelationsLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cr_lifecycle_role"})
	p := fixture.NewScenario(t)
	var emitted []byte
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) {
		emitted = catalogRelationsLifecycle(t, p, b, emitted)
	})
}

// The SQL contract is shared. Native alone owns host replacement controls;
// PostgreSQL must replay the exact nonempty dump captured by the native phase.
func catalogRelationsLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, emitted []byte) []byte {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE ROLE cr_lifecycle_role NOLOGIN", 0))
	if b.PG && len(emitted) == 0 {
		t.Fatal("final PostgreSQL phase requires actual native dump bytes")
	}
	if b.PG {
		if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native dump replay", err)
		}
	} else {
		run(fixture.Exec("CREATE TABLE cr_lifecycle_data (id integer, stored name, missing name DEFAULT pg_get_userbyid(0))", 0))
		run(fixture.Exec("INSERT INTO cr_lifecycle_data(id,stored) VALUES (7,'cr_lifecycle_role')", 1))
		run(fixture.Exec("CREATE VIEW cr_lifecycle_scalar AS SELECT pg_get_userbyid(0) AS missing,pg_get_userbyid(NULL::oid) AS absent", 0))
		run(fixture.Exec("CREATE VIEW cr_lifecycle_current AS SELECT pg_get_userbyid(oid) AS n FROM pg_catalog.pg_roles WHERE rolname=current_user", 0))
		run(fixture.Exec("CREATE VIEW cr_lifecycle_links AS SELECT fktable::oid AS fktable,fkcols,pktable::oid AS pktable,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys()", 0))
	}
	check := func() {
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT missing,absent FROM cr_lifecycle_scalar", []string{"missing", "absent"}, []string{"NAME", "NAME"}, [][]any{{"unknown (OID=0)", nil}}),
				fixture.Select("SELECT n=current_user AS same FROM cr_lifecycle_current", []string{"same"}, []string{"BOOL"}, [][]any{{true}}),
				fixture.Select("SELECT id,stored,missing FROM cr_lifecycle_data ORDER BY id", []string{"id", "stored", "missing"}, []string{"INT4", "NAME", "NAME"}, [][]any{{int64(7), "cr_lifecycle_role", "unknown (OID=0)"}}),
			} {
				s.Prepared = prepared
				run(s)
			}
			s := catalogRelationsInventoryStep("cr_lifecycle_links")
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		}
		oid := catalogRelationsRoleOID(t, p, b.DB, "cr_lifecycle_role")
		run(fixture.Select("SELECT pg_get_userbyid($1) AS n", []string{"n"}, []string{"NAME"}, [][]any{{"cr_lifecycle_role"}}, oid))
	}
	check()
	run(fixture.Select("SELECT CASE WHEN pg_get_userbyid(NULL::oid) IS NULL THEN pg_get_userbyid(0) ELSE NULL END AS n", []string{"n"}, []string{"NAME"}, [][]any{{"unknown (OID=0)"}}))
	run(fixture.Select("SELECT pg_get_userbyid(0) AS n,count(*) AS total FROM cr_lifecycle_data GROUP BY pg_get_userbyid(0) ORDER BY pg_get_userbyid(0)", []string{"n", "total"}, []string{"NAME", "INT8"}, [][]any{{"unknown (OID=0)", int64(1)}}))
	fixture.ViewCopy(t, p, b, "cr_lifecycle_scalar", "cr_lifecycle_scalar_copy")
	run(fixture.Select("SELECT missing,absent FROM cr_lifecycle_scalar_copy", []string{"missing", "absent"}, []string{"NAME", "NAME"}, [][]any{{"unknown (OID=0)", nil}}))
	run(fixture.Exec("DROP VIEW cr_lifecycle_scalar_copy", 0))
	if b.PG {
		return emitted
	}
	retained, err := p.Native.QueryContext(p.Ctx, "SELECT missing,absent FROM cr_lifecycle_scalar")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	held := fixture.ViewHold(t, p, b, "SELECT missing,absent FROM cr_lifecycle_scalar")
	dump := fixture.ViewDump(t, p)
	if !bytes.Contains(dump, []byte("pg_get_userbyid")) || !bytes.Contains(dump, []byte("pg_get_catalog_foreign_keys")) {
		t.Fatal("dump lost symbolic selected calls", string(dump))
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.ViewRestore(t, p, dump)
	check()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"missing", "absent"}, []string{"NAME", "NAME"}, [][]any{{"unknown (OID=0)", nil}}))
	types, err := retained.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "NAME" || types[1].DatabaseTypeName() != "NAME" {
		t.Fatal(types, err)
	}
	if !retained.Next() {
		t.Fatal("retained row lost", retained.Err())
	}
	var missing string
	var absent sql.NullString
	if err := retained.Scan(&missing, &absent); err != nil || missing != "unknown (OID=0)" || absent.Valid {
		t.Fatal(missing, absent, err)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal("retained cursor", retained.Err())
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE cr_bad(i integer); INSERT INTO cr_bad VALUES ('bad');")); err == nil {
		t.Fatal("invalid restore published")
	}
	check()
	// Context errors remain ordinary Go errors, not synthesized SQLSTATEs.
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
	defer expire()
	for _, sample := range []struct {
		ctx  context.Context
		want error
	}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
		for _, q := range []string{"SELECT pg_get_userbyid(0)", "SELECT * FROM pg_get_catalog_foreign_keys()"} {
			rows, err := b.DB.QueryContext(sample.ctx, q)
			if rows != nil {
				rows.Close()
				t.Fatal("failed context exposed rows")
			}
			if !errors.Is(err, sample.want) {
				t.Fatal(err, sample.want)
			}
		}
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE cr_lifecycle_role NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	freshOID := catalogRelationsRoleOID(t, p, fresh, "cr_lifecycle_role")
	var got string
	if err := fresh.QueryRowContext(p.Ctx, "SELECT pg_get_userbyid($1)", freshOID).Scan(&got); err != nil || got != "cr_lifecycle_role" {
		t.Fatal(got, err)
	}
	if err := fresh.QueryRowContext(p.Ctx, "SELECT missing FROM cr_lifecycle_scalar").Scan(&got); err != nil || got != "unknown (OID=0)" {
		t.Fatal(got, err)
	}
	rows, err := fresh.QueryContext(p.Ctx, "SELECT fktable,fkcols,pktable,pkcols,is_array,is_opt FROM cr_lifecycle_links")
	if err != nil {
		t.Fatal(err)
	}
	// Unwrapped public control pool: validate the actual complete typed stream.
	defer rows.Close()
	var actual [][]any
	for rows.Next() {
		cells := make([]any, 6)
		dest := make([]any, 6)
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range cells {
			fixture.AssertRawValue(t, false, []string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}[i], v)
			cells[i] = fixture.Normalize(v)
		}
		actual = append(actual, cells)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if _, ok := fixture.CompareRows(actual, catalogRelationsRelease219, true); !ok {
		t.Fatal("fresh-driver inventory differs", actual)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return dump
}
