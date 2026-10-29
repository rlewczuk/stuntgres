package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicaIdentityNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ri_dump_owner"})
	fixture.SchemaReferenceCleanup(t, "ri_dump")
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE ROLE ri_dump_owner", 0))
	p.Step(t, fixture.Exec("CREATE SCHEMA ri_dump AUTHORIZATION ri_dump_owner", 0))
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("SET SESSION AUTHORIZATION ri_dump_owner", 0))
			for _, q := range []string{
				"CREATE TABLE ri_dump.ri_full(k INT NOT NULL,b BIGINT,p TEXT)",
				"CREATE TABLE ri_dump.ri_nothing(k INT)", "CREATE TABLE ri_dump.ri_default(k INT)",
				"CREATE TABLE ri_dump.ri_attached(k INT CONSTRAINT ri_attached_pk PRIMARY KEY)",
				"CREATE TABLE ri_dump.ri_unique(k INT NOT NULL CONSTRAINT ri_unique_u UNIQUE)",
				`CREATE TABLE ri_dump."ri quoted"(k INT NOT NULL,b INT NOT NULL,p TEXT)`,
				`CREATE UNIQUE INDEX "ri""index" ON ri_dump."ri quoted"(b DESC NULLS FIRST,k) INCLUDE(p)`,
				"ALTER TABLE ri_dump.ri_full REPLICA IDENTITY FULL",
				"ALTER TABLE ri_dump.ri_nothing REPLICA IDENTITY NOTHING",
				"ALTER TABLE ri_dump.ri_default REPLICA IDENTITY DEFAULT",
				"ALTER TABLE ri_dump.ri_attached REPLICA IDENTITY USING INDEX ri_attached_pk",
				"ALTER TABLE ri_dump.ri_unique REPLICA IDENTITY USING INDEX ri_unique_u",
				`ALTER TABLE ri_dump."ri quoted" REPLICA IDENTITY USING INDEX "ri""index"`,
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO ri_dump.ri_full VALUES(1,9223372036854775807,'one'),(2,NULL,NULL)", 2))
			run(fixture.Exec("INSERT INTO ri_dump.ri_attached VALUES(5)", 1))
			run(fixture.Exec("INSERT INTO ri_dump.ri_unique VALUES(6)", 1))
			run(fixture.Exec(`INSERT INTO ri_dump."ri quoted" VALUES(1,2,'one'),(3,4,NULL)`, 2))
		}()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname='ri_full'", []string{"relname"}, []string{"NAME"}, nil))
	temporary, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer temporary.Close()
	tempRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, temporary, p.MemoryRecord, s) }
	tempRun(fixture.Exec("CREATE TEMP TABLE ri_full(k INT NOT NULL)", 0))
	tempRun(fixture.Exec("CREATE UNIQUE INDEX ri_temp_key ON ri_full(k)", 0))
	tempRun(fixture.Exec("ALTER TABLE ri_full REPLICA IDENTITY USING INDEX ri_temp_key", 0))
	tempRun(fixture.Exec("INSERT INTO ri_full VALUES(9)", 1))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tempRun(replicaMode("ri_full", "i"))
	tempRun(fixture.Select("SELECT k FROM ri_full", []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA ri_dump CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual native bytes replay", err)
		}
	}
	check := func(run func(fixture.Step)) {
		for _, tc := range []struct{ name, mode string }{{"ri_full", "f"}, {"ri_nothing", "n"}, {"ri_default", "d"}, {"ri_attached", "i"}, {"ri_unique", "i"}, {`"ri quoted"`, "i"}} {
			run(replicaMode("ri_dump."+tc.name, tc.mode))
		}
		run(fixture.Select("SELECT c.relname,i.indisreplident,p.relname AS parent FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid JOIN pg_namespace n ON n.oid=p.relnamespace WHERE n.nspname='ri_dump' ORDER BY c.relname", []string{"relname", "indisreplident", "parent"}, []string{"NAME", "BOOL", "NAME"}, [][]any{{`ri"index`, true, "ri quoted"}, {"ri_attached_pk", true, "ri_attached"}, {"ri_unique_u", true, "ri_unique"}}))
		data := fixture.Select("SELECT k,b,p FROM ri_dump.ri_full ORDER BY k", []string{"k", "b", "p"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(1), int64(9223372036854775807), "one"}, {int64(2), nil, nil}})
		run(data)
		run(fixture.Prepared(data))
		run(fixture.Select(`SELECT k,b,p FROM ri_dump."ri quoted" ORDER BY k`, []string{"k", "b", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "one"}, {int64(3), int64(4), nil}}))
		run(fixture.ErrorDiagnostic("INSERT INTO ri_dump.ri_attached VALUES(5)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "ri_attached_pk"`, Detail: "Key (k)=(5) already exists.", Schema: "ri_dump", Table: "ri_attached", Constraint: "ri_attached_pk"}))
	}
	check(func(s fixture.Step) { p.Step(t, s) })
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("noncanonical identity replay", err, again.String())
	}
	// Destination state and already-open snapshots survive a rejected replacement.
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE ri_sentinel(k INT)", 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO ri_sentinel VALUES(7)", 1))
	held, err := p.Native.QueryContext(p.Ctx, "SELECT k,b,p FROM ri_dump.ri_full ORDER BY k")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	catalogRows, err := p.Native.QueryContext(p.Ctx, "SELECT relreplident FROM pg_class WHERE oid='ri_dump.ri_full'::regclass")
	if err != nil {
		t.Fatal(err)
	}
	defer catalogRows.Close()
	bad := dump.String() + "ALTER TABLE ri_dump.ri_full REPLICA IDENTITY USING INDEX ri_missing;\n"
	want := fixture.ControlsReferenceDiagnostic("42704", `index "ri_missing" for table "ri_full" does not exist`)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"ALTER TABLE ri_dump.ri_full REPLICA IDENTITY FULL junk;")); err == nil {
		t.Fatal("malformed restore accepted")
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT k FROM ri_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
	check(func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) })
	for _, row := range [][]any{{int64(1), int64(9223372036854775807), "one"}, {int64(2), nil, nil}} {
		if !held.Next() {
			t.Fatal("held snapshot lost", held.Err())
		}
		var k, b, p any
		if err := held.Scan(&k, &b, &p); err != nil || k != row[0] || b != row[1] || fixture.Normalize(p) != row[2] {
			t.Fatal(k, b, p, err)
		}
	}
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	if !catalogRows.Next() {
		t.Fatal("held catalog snapshot lost", catalogRows.Err())
	}
	var mode string
	if err := catalogRows.Scan(&mode); err != nil || mode != "f" {
		t.Fatal("held catalog mode", mode, err)
	}
	if catalogRows.Next() || catalogRows.Err() != nil {
		t.Fatal(catalogRows.Err())
	}
}
