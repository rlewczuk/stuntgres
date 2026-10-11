package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func domainReferenceRoundtripRows() fixture.Step {
	return fixture.Select("SELECT id,i,n,v,a,c FROM domain_s.\"Stored Values\" ORDER BY id", []string{"id", "i", "n", "v", "a", "c"}, []string{"INT4", "INT4", "NUMERIC", "VARCHAR", "_NAME", "INT4"}, [][]any{
		{int64(1), int64(7), "12.35", "abc", `{NULL,"NULL","",alpha}`, int64(7)},
		{int64(2), nil, nil, nil, nil, nil},
		{int64(3), int64(8), "1.20", "xy", `{{a,b},{c,NULL}}`, int64(8)},
		{int64(4), int64(9), "2.00", "z", `[0:1]={a,b}`, int64(9)},
	})
}

func TestReferenceDomainNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"domain_roundtrip_owner"})
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		if backend.PG {
			if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			if _, err := backend.DB.ExecContext(p.Ctx, `DROP DOMAIN IF EXISTS public."Integer Domain" CASCADE`); err != nil {
				t.Error(err)
			}
		})
		for _, q := range []string{
			"CREATE ROLE domain_roundtrip_owner",
			"SET ROLE domain_roundtrip_owner",
			"CREATE DOMAIN public.\"Integer Domain\" AS integer",
			"RESET ROLE",
			"CREATE DOMAIN domain_s.\"Number Domain\" AS numeric(5,2)",
			"CREATE DOMAIN domain_s.\"String Domain\" AS varchar(3)",
			"CREATE DOMAIN domain_s.\"Array Domain\" AS name[]",
			"CREATE DOMAIN domain_s.\"Chain Domain\" AS public.\"Integer Domain\"",
			"CREATE TABLE domain_s.\"Stored Values\" (id integer,i public.\"Integer Domain\",n domain_s.\"Number Domain\",v domain_s.\"String Domain\",a domain_s.\"Array Domain\",c domain_s.\"Chain Domain\" DEFAULT 7::domain_s.\"Chain Domain\")",
			"CREATE VIEW domain_s.\"Domain View\" AS SELECT id,(i+1)::public.\"Integer Domain\" AS v FROM domain_s.\"Stored Values\"",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.\"Stored Values\" (id,i,n,v,a) VALUES (1,7,12.345,'abc','{NULL,\"NULL\",\"\",alpha}')", 1))
		run(fixture.Exec("INSERT INTO domain_s.\"Stored Values\" VALUES (2,NULL,NULL,NULL,NULL,NULL)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.\"Stored Values\" VALUES (3,$1,$2,$3,$4,$5)", 1, int64(8), "1.2", "xy", "{{a,b},{c,NULL}}", int64(8))))
		run(fixture.Exec("INSERT INTO domain_s.\"Stored Values\" VALUES (4,9,2,'z','[0:1]={a,b}',9)", 1))
		check := func(executor fixture.Executor, recorder *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, executor, recorder, domainReferenceRoundtripRows())
			fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Prepared(domainReferenceRoundtripRows()))
			fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select("SELECT id,v FROM domain_s.\"Domain View\" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), nil}, {int64(3), int64(9)}, {int64(4), int64(10)}}))
		}
		check(conn, recorder)
		run(fixture.Exec("CREATE TEMP SEQUENCE domain_temp", 0))
		run(fixture.Select("SELECT nextval('domain_temp') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		// PostgreSQL has no native process-local Dump/Reset/Restore API.
		// Its independent ordinary SQL witnesses above remain the shared contract.
		if backend.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		check(conn, recorder) // Dump must not change source values or dependencies.
		// Retain eager rows and analyzed metadata across both native replacements.
		retained, err := backend.DB.QueryContext(p.Ctx, domainReferenceRoundtripRows().Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		domainReferenceCheckMetadata(t, retained, false, false, domainReferenceRoundtripRows().Types)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Error("SELECT * FROM domain_s.\"Stored Values\"", "42P01"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET search_path TO public", 0))
		check(conn, recorder)
		run(fixture.Select("SELECT nextval('domain_temp') AS v FROM public.one", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
		// Restored domain IDs may remint: fresh direct/prepared reads above are
		// required, not success of a held statement with an old logical signature.
		domainReferenceCheckMetadata(t, retained, false, false, domainReferenceRoundtripRows().Types)
		var oldRows [][]any
		for retained.Next() {
			row := make([]any, 6)
			dest := make([]any, 6)
			for i := range row {
				dest[i] = &row[i]
			}
			if err := retained.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for i := range row {
				row[i] = fixture.Normalize(row[i])
			}
			oldRows = append(oldRows, row)
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(oldRows, domainReferenceRoundtripRows().Rows) {
			t.Fatalf("retained rows=%#v", oldRows)
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		// Reuse the existing fixture's fresh-driver branch, not a new harness.
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE remap_spacer", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE domain_roundtrip_owner", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		check(target.Memory, target.MemoryRecord)
		var second bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second.String())); err != nil {
			t.Fatal(err)
		}
		check(target.Memory, target.MemoryRecord)
		// Atomic failed replacement preserves a pre-existing independent target.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.sentinel (v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.sentinel VALUES (42)", 1))
		bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t (v staged.d); INSERT INTO staged.t VALUES ('bad');"
		err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
		fixture.State(t, err, "22P02")
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT 1::staged.d FROM public.sentinel", "3F000"))
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		check(target.Memory, target.MemoryRecord)
		ownerConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer ownerConn.Close()
		for _, s := range []fixture.Step{
			fixture.Exec("SET ROLE remap_spacer", 0),
			fixture.Error("DROP DOMAIN public.\"Integer Domain\"", "42501"),
			fixture.Exec("RESET ROLE", 0),
			fixture.Exec("SET ROLE domain_roundtrip_owner", 0),
			fixture.Error("DROP DOMAIN public.\"Integer Domain\"", "2BP01"),
			fixture.Exec("RESET ROLE", 0),
		} {
			fixture.RunStep(t, target.Ctx, ownerConn, target.MemoryRecord, s)
		}
		// Canceled domain DDL cannot publish a type or erase existing columns.
		if _, err := conn.ExecContext(ctx, "CREATE DOMAIN domain_s.canceled AS integer"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled CREATE=%v", err)
		}
		if _, err := conn.ExecContext(ctx, "DROP DOMAIN public.\"Integer Domain\" CASCADE"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled DROP=%v", err)
		}
		run(fixture.Error("SELECT NULL::domain_s.canceled FROM public.one", "42704"))
		check(conn, recorder)
	})
}
