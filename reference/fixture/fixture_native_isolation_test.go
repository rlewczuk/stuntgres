package fixture

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"stuntgres"
	"testing"

	"github.com/ngrok/sqlmw"
)

func TestReferenceFixtureNativeIsolation(t *testing.T) {
	for _, mode := range []struct {
		name      string
		nativeRaw bool
	}{{"legacy", false}, {"native_raw", true}} {
		t.Run(mode.name, func(t *testing.T) {
			first := NewFixture(t, *Mode, mode.nativeRaw)
			wantMemoryRaw, wantPostgresRaw := RawLegacy, RawLegacy
			if mode.nativeRaw {
				wantMemoryRaw, wantPostgresRaw = RawNative, RawPostgres
			}
			if first.MemoryRecord.RawBackend != wantMemoryRaw || first.PostgresRecord.RawBackend != wantPostgresRaw {
				t.Fatal("fixture changed recorder provenance")
			}
			first.Step(t, Exec("CREATE TABLE isolated(v INT)", 0))
			first.Step(t, Exec("INSERT INTO isolated VALUES (7)", 1))
			var value int64
			if err := first.Native.QueryRowContext(first.Ctx, "SELECT v FROM isolated").Scan(&value); err != nil || value != 7 {
				t.Fatalf("native control pool shares wrapped state: value=%d error=%v", value, err)
			}
			for _, prepared := range []bool{false, true} {
				step := Select("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				step.Prepared = prepared
				first.Step(t, step)
			}
			if mode.nativeRaw {
				for _, backend := range first.Backends() {
					t.Run("scenario_notice/"+backend.Name, func(t *testing.T) {
						var notices []Diagnostic
						_, peer, record := NoticeReferencePool(t, first, backend.PG, &notices)
						RunStep(t, first.Ctx, peer, record, Select("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
						RunStep(t, first.Ctx, peer, record, Exec("UPDATE isolated SET v=9", 1))
						RunStep(t, first.Ctx, backend.DB, backend.Recorder, Select("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
						RunStep(t, first.Ctx, backend.DB, backend.Recorder, Exec("UPDATE isolated SET v=7", 1))
					})
				}
			}

			// Native host lifetime is Driver/exact-DSN lifetime, not pool lifetime.
			if err := first.Memory.Close(); err != nil {
				t.Fatal(err)
			}
			if err := first.Native.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := first.NativeConnector.Driver().(driver.DriverContext).OpenConnector("reference/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			first.Native = sql.OpenDB(connector)
			wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
			configured, err := wrapped.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			first.Memory = sql.OpenDB(configured)
			if err := first.Native.QueryRowContext(first.Ctx, "SELECT v FROM isolated").Scan(&value); err != nil || value != 7 {
				t.Fatalf("reopened native control pool: value=%d error=%v", value, err)
			}
			distinctConnector, err := first.NativeConnector.Driver().(driver.DriverContext).OpenConnector("reference/" + t.Name() + "/distinct")
			if err != nil {
				t.Fatal(err)
			}
			distinct := sql.OpenDB(distinctConnector)
			t.Cleanup(func() {
				if err := distinct.Close(); err != nil {
					t.Error(err)
				}
			})
			err = distinct.QueryRowContext(first.Ctx, "SELECT v FROM isolated").Scan(&value)
			wantMissing := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "isolated" does not exist`, Position: "15"}
			if err == nil || ExtractDiagnostic(t, err) != wantMissing {
				t.Fatal("different DSN inherited fixture table", err)
			}

			// A separate fixture owns a separate Driver even for this exact DSN.
			// PostgreSQL has no per-Driver database equivalent; this is a native
			// control phase after the shared SQL witnesses above.
			second := NewFixture(t, false, mode.nativeRaw)
			second.Step(t, ImplicitError("SELECT v FROM isolated", wantMissing))
			second.Step(t, Exec("CREATE TABLE isolated(v INT)", 0))
			second.Step(t, Select("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, nil))
			first.Step(t, Select("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceFixtureResetIsolation(t *testing.T) {
	t.Run("contaminate", func(t *testing.T) {
		p := NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, query := range []string{
					"SET SESSION AUTHORIZATION postgres",
					"SET application_name='fixture-contaminated'",
					"CREATE TABLE public.fixture_reset_probe(v INT)",
				} {
					RunStep(t, p.Ctx, c, b.Recorder, Exec(query, 0))
				}
				if b.PG {
					// A replay can return an admin session with the wrong
					// principal. Boundary sanitation must restore its original
					// authenticator before recreating the public schema.
					admin, err := Admin.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					if _, err := admin.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION postgres"); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	})
	t.Run("next_case", func(t *testing.T) {
		p := NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				principal := "postgres"
				if b.PG {
					principal = "stuntgres"
				}
				run := func(step Step) {
					RunStep(t, p.Ctx, b.DB, b.Recorder, step)
				}
				run(Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{principal, principal}}))
				run(Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
				if b.PG {
					// pg_namespace is an oracle surface, not a native catalog.
					run(Select("SELECT pg_get_userbyid(nspowner) AS owner FROM pg_namespace WHERE nspname='public'", []string{"owner"}, []string{"NAME"}, [][]any{{principal}}))
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if dump.Len() != 0 {
						t.Fatalf("fresh native fixture retained catalog state: %s", dump.String())
					}
				}
				// Successful same-name creation and the empty result independently
				// establish that no previous fixture table or data was retained.
				run(Exec("CREATE TABLE public.fixture_reset_probe(v INT)", 0))
				run(Select("SELECT v FROM public.fixture_reset_probe", []string{"v"}, []string{"INT4"}, nil))
				if !b.PG {
					// Native host control independently clears actual catalog/data
					// without PostgreSQL-only namespace introspection.
					run(Exec("INSERT INTO public.fixture_reset_probe VALUES(7)", 1))
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if dump.Len() != 0 {
						t.Fatalf("native reset retained catalog state: %s", dump.String())
					}
					run(Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{principal, principal}}))
					run(Exec("CREATE TABLE public.fixture_reset_probe(v INT)", 0))
					run(Select("SELECT v FROM public.fixture_reset_probe", []string{"v"}, []string{"INT4"}, nil))
				}
			})
		}
	})
}
