package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceConversionTransactionDefaults(t *testing.T) {
	for _, reverseCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_creator_first", true: "second_creator_first"}[reverseCommit], func(t *testing.T) {
			conversionScenario(t, []string{"conv_pair"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conversionExec(t, p, b, "CREATE SCHEMA conv_pair")
				a := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer a.ExecContext(p.Ctx, "ROLLBACK")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
				run(a, fixture.Exec("BEGIN", 0))
				run(c, fixture.Exec("BEGIN", 0))
				run(a, fixture.Exec(fixture.ConversionCreate("conv_pair.a", true), 0))
				// This is deliberately not a serial uniqueness assertion: neither
				// transaction can see the other's row. The released index includes OID.
				run(c, fixture.Exec(fixture.ConversionCreate("conv_pair.b", true), 0))
				if reverseCommit {
					run(c, fixture.Exec("COMMIT", 0))
					run(a, fixture.Exec("COMMIT", 0))
				} else {
					run(a, fixture.Exec("COMMIT", 0))
					run(c, fixture.Exec("COMMIT", 0))
				}
				run(a, fixture.Select("SELECT conname,conforencoding,contoencoding,condefault FROM pg_conversion WHERE connamespace='conv_pair'::regnamespace ORDER BY conname", []string{"conname", "conforencoding", "contoencoding", "condefault"}, []string{"NAME", "INT4", "INT4", "BOOL"}, [][]any{{"a", int64(6), int64(8), true}, {"b", int64(6), int64(8), true}}))
				run(a, fixture.Exec("BEGIN", 0))
				run(a, fixture.Exec("SAVEPOINT duplicate", 0))
				run(a, fixture.ErrorDiagnostic(fixture.ConversionCreate("conv_pair.c", true), conversionDiagnostic("42710", "default conversion for UTF8 to LATIN1 already exists")))
				run(a, fixture.Exec("ROLLBACK TO duplicate", 0))
				run(a, conversionCount("c", 0))
				run(a, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}

func TestReferenceConversionTransactionConsumers(t *testing.T) {
	conversionScenario(t, []string{"conv_bytes_hidden"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, "CREATE SCHEMA conv_bytes_hidden")
		conversionHideSeed(t, p, b, "conv_bytes_hidden")
		conversionHideNamedSeed(t, p, b, "conv_bytes_hidden", "iso_8859_1_to_utf8")
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE SCHEMA conv_private", 0))
		run(fixture.Exec(fixture.ConversionCreate("conv_private.nondefault", false), 0))
		run(fixture.Exec("SET LOCAL search_path=conv_private,public", 0))
		run(fixture.Exec("SAVEPOINT missing", 0))
		missing := fixture.ErrorDiagnostic(conversionValue().Query, conversionMissingDefault())
		missing.SelectRows = true
		run(missing)
		run(fixture.Exec("ROLLBACK TO missing", 0))
		run(fixture.Exec(fixture.ConversionCreate("conv_private.forward", true), 0))
		run(fixture.Exec("CREATE DEFAULT CONVERSION conv_private.reverse FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8", 0))
		run(conversionValue())
		run(fixture.Select("SELECT convert_from(decode('e9','hex'),'LATIN1') AS text,convert(decode('e9','hex'),'LATIN1','UTF8') AS bytes", []string{"text", "bytes"}, []string{"TEXT", "BYTEA"}, [][]any{{"é", "é"}}))
		for _, s := range []fixture.Step{fixture.ErrorDiagnostic("SELECT convert_to('€','LATIN1')", conversionEuro()), fixture.ErrorDiagnostic("SELECT convert(decode('ff','hex'),'UTF8','LATIN1')", conversionDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`))} {
			run(fixture.Exec("SAVEPOINT bad_bytes", 0))
			s.SelectRows = true
			run(s)
			run(fixture.ErrorDiagnostic("SELECT 1", conversionDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO bad_bytes", 0))
			run(conversionValue())
			run(fixture.Exec("RELEASE bad_bytes", 0))
		}
		run(fixture.Exec("SAVEPOINT dropped", 0))
		run(fixture.Exec("DROP CONVERSION conv_private.forward", 0))
		run(missing)
		run(fixture.Exec("ROLLBACK TO dropped", 0))
		run(conversionValue())
		run(fixture.Exec("RELEASE dropped", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(conversionCount("forward", 0))
		run(conversionCount("reverse", 0))
		run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='conv_private'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceConversionTransactionHeldAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"conv_tx_actor", "conv_tx_other"})
	conversionScenario(t, []string{"conv_tx_auth"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, "CREATE ROLE conv_tx_actor NOSUPERUSER", "CREATE ROLE conv_tx_other NOSUPERUSER", "CREATE SCHEMA conv_tx_auth", "GRANT USAGE,CREATE ON SCHEMA conv_tx_auth TO conv_tx_actor", fixture.ConversionCreate("conv_tx_auth.existing", false), "ALTER CONVERSION conv_tx_auth.existing OWNER TO conv_tx_actor")
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, q := range []string{fixture.ConversionCreate("conv_tx_auth.fresh", false), "ALTER CONVERSION conv_tx_auth.existing RENAME TO stolen", "DROP CONVERSION conv_tx_auth.existing"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE conv_tx_actor", 0))
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if q[0] == 'C' {
				conversionExec(t, p, b, "REVOKE CREATE ON SCHEMA conv_tx_auth FROM conv_tx_actor")
			} else {
				conversionExec(t, p, b, "ALTER CONVERSION conv_tx_auth.existing OWNER TO conv_tx_other")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			message := "permission denied for schema conv_tx_auth"
			if q[0] == 'A' {
				message = "must be owner of conversion existing"
			}
			if q[0] == 'D' {
				message = "must be owner of conversion conv_tx_auth.existing"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, conversionDiagnostic("42501", message)))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			stmt.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			conversionExec(t, p, b, "GRANT CREATE ON SCHEMA conv_tx_auth TO conv_tx_actor", "ALTER CONVERSION conv_tx_auth.existing OWNER TO conv_tx_actor")
		}
		// Native cannot name the sixarg internal/cstring ABI in ACL SQL. Preserve
		// that existing boundary; the real PostgreSQL authority witness is not a fake.
		if b.PG {
			const signature = "pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)"
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := b.DB.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
					t.Error(err)
				}
			})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE conv_tx_actor", 0))
			q := fixture.ConversionCreate("conv_tx_auth.noexecute", false)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			conversionExec(t, p, b, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, conversionDiagnostic("42501", "permission denied for function pg_catalog.utf8_to_iso8859_1")))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
	})
}

func TestReferenceConversionTransactionNotices(t *testing.T) {
	conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		q := "DROP CONVERSION IF EXISTS conv_tx_absent"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(notices) != 0 {
			t.Fatal("Prepare notices", notices)
		}
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, rec, fixture.Exec(q, 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `conversion "conv_tx_absent" does not exist, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(end, 0))
			notices = nil
		}
	})
}
