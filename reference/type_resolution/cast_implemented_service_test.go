package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const castServiceDDL = `CREATE TABLE PUBLIC.SERVICE_CERTIFICATES (ID uuid, SERVICE text, CERTIFICATE text, CERT_ACTIVE boolean, VALID_DATE timestamptz)`
const castServiceInsert = `insert into PUBLIC.SERVICE_CERTIFICATES (ID, SERVICE, CERTIFICATE, CERT_ACTIVE, VALID_DATE) values (cast($1 as uuid), $2, $3, $4, cast($5 as timestamp(6) with time zone))`
const castServiceUUID = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
const castServiceOtherUUID = "00000000-0000-0000-0000-000000000002"

// Values follow PG18 UUID input and timestamp precision rules, not either
// backend's output: 2004-01-01 midnight +02 is the preceding day at 22:00 UTC.
func castServiceRows() [][]any {
	return [][]any{
		{castServiceUUID, "svc-a", "cert-a", true, time.Date(2003, 12, 31, 22, 0, 0, 123456000, time.UTC)},
		{castServiceUUID, "svc-b", "cert-b", false, time.Date(2003, 12, 31, 22, 0, 0, 123456000, time.UTC)},
		{castServiceOtherUUID, "svc-c", "cert-c", true, time.Date(2004, 7, 1, 16, 0, 0, 654321000, time.UTC)},
		{nil, "svc-null", "cert-null", false, nil},
	}
}

func castServiceSelect() fixture.Step {
	return fixture.Select("SELECT id,service,certificate,cert_active,valid_date FROM public.service_certificates ORDER BY service",
		[]string{"id", "service", "certificate", "cert_active", "valid_date"},
		[]string{"UUID", "TEXT", "TEXT", "BOOL", "TIMESTAMPTZ"}, castServiceRows())
}

func TestReferenceCastImplementedServiceCertificatesSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			run(fixture.Exec(castServiceDDL, 0))
			run(fixture.Exec(castServiceInsert, 1, "{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", "svc-a", "cert-a", true, "2004-01-01 00:00:00.123456+02"))
			stmt, err := conn.PrepareContext(p.Ctx, castServiceInsert)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: castServiceInsert}
			for _, args := range [][]any{
				{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", "svc-b", "cert-b", false, "2004-01-01 00:00:00.123456+02"},
				{castServiceOtherUUID, "svc-c", "cert-c", true, "2004-07-01 12:00:00.654321-04"},
				{nil, "svc-null", "cert-null", false, nil},
			} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(castServiceInsert, 1, args...))
			}
			run(castServiceSelect())
			// This is a typed TEXT runtime conversion, unlike inferred UUID Bind input.
			where := "SELECT service FROM public.service_certificates WHERE id=CAST($1::text AS pg_catalog.uuid) ORDER BY service"
			filter, err := conn.PrepareContext(p.Ctx, where)
			if err != nil {
				t.Fatal(err)
			}
			defer filter.Close()
			for _, x := range []fixture.Executor{conn, fixture.HeldStatement{Executor: conn, Statement: filter, SQL: where}} {
				for _, tc := range []struct {
					arg  any
					rows [][]any
				}{
					{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", [][]any{{"svc-a"}, {"svc-b"}}},
					{castServiceOtherUUID, [][]any{{"svc-c"}}}, {nil, nil},
				} {
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, fixture.Select(where, []string{"service"}, []string{"TEXT"}, tc.rows, tc.arg))
				}
			}
			for _, x := range []fixture.Executor{conn, held} {
				for _, tc := range []struct {
					args                   []any
					code, message, context string
				}{
					{[]any{"bad", "bad-id", "cert", true, "2004-01-01+00"}, "22P02", `invalid input syntax for type uuid: "bad"`, "unnamed portal parameter $1 = '...'"},
					{[]any{castServiceUUID, "bad-date", "cert", true, "nonsense"}, "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, "unnamed portal parameter $5 = '...'"},
				} {
					s := fixture.ImplicitError(castServiceInsert, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: tc.context}, tc.args...)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, s)
					run(castServiceSelect())
				}
			}
			// The second row fails at runtime, after the first row has been formed.
			// TEXT parameters avoid a Bind-time failure hiding DML atomicity.
			multi := `INSERT INTO public.service_certificates VALUES (CAST($1::text AS uuid),'late-a','cert',true,CAST($2::text AS timestamptz)),(CAST($3::text AS uuid),'late-b','cert',true,CAST($4::text AS timestamptz))`
			multiStmt, err := conn.PrepareContext(p.Ctx, multi)
			if err != nil {
				t.Fatal(err)
			}
			defer multiStmt.Close()
			for _, x := range []fixture.Executor{conn, fixture.HeldStatement{Executor: conn, Statement: multiStmt, SQL: multi}} {
				for _, tc := range []struct{ id, date, code, message string }{
					{"bad", "2004-01-01+00", "22P02", `invalid input syntax for type uuid: "bad"`},
					{castServiceUUID, "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`},
				} {
					s := fixture.ImplicitError(multi, fixture.Diagnostic{Code: tc.code, Message: tc.message}, castServiceUUID, "2004-01-01+00", tc.id, tc.date)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, s)
					run(castServiceSelect())
				}
			}
			// Native controls have no SQL counterpart. Preserve the same independently
			// specified table bag and metadata on this owner and a fresh owner.
			if !backend.PG {
				castServiceControls(t, p, run)
			}
		})
	}
}

func castServiceControls(t *testing.T, p *fixture.Pair, run func(fixture.Step)) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(castServiceSelect())
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh-restore")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	recorded, freshRecorder := fixture.RecordedNativePool(t, connector)
	fixture.RunStep(t, p.Ctx, recorded, freshRecorder, castServiceSelect())
	bad := dump.String() + `; CREATE TABLE bad_restore(v uuid); INSERT INTO bad_restore VALUES ('00000000-0000-0000-0000-000000000001'),('bad');`
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type uuid: "bad"`)
	// Restore reports the source stream's character position, including the dump.
	want.Position = fixture.Position(strings.LastIndex(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, err, &want)
	run(castServiceSelect())
}
