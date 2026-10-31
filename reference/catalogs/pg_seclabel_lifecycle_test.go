package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgSeclabelLifecycle(t *testing.T) {
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgSeclabelSetup(t, p, b, c)
		pgSeclabelIdentity(t, p, b, c)
		held := pgSeclabelHeld(t, p, c, pgSeclabelSelect)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL))
		q := "SECURITY LABEL ON COLUMN public.sl_sentinel.c IS 'secret'"
		pgSeclabelError(t, p, b, c, q, pgSeclabelAbsent())
		pgSeclabelExec(t, p, b, c, "ALTER TABLE public.sl_sentinel RENAME TO sl_renamed", "ALTER TABLE public.sl_renamed DROP COLUMN b")
		pgSeclabelError(t, p, b, c, "SECURITY LABEL FOR missing ON COLUMN public.sl_renamed.c IS NULL", pgSeclabelProvider("missing"))
		pgSeclabelEmpty(t, p, b, c)
		pgSeclabelExec(t, p, b, c, "ALTER TABLE public.sl_renamed RENAME TO sl_sentinel")
		pgSeclabelSentinel(t, p, b, c)

		if b.PG {
			// Real SQL lifecycle, not a fictional PostgreSQL Reset API.
			pgSeclabelExec(t, p, b, c, "DROP TABLE public.sl_sentinel", "CREATE TABLE public.sl_sentinel(a INT,c INT)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO public.sl_sentinel VALUES(7,9)", 1))
			pgSeclabelError(t, p, b, c, q, pgSeclabelAbsent())
			pgSeclabelSentinel(t, p, b, c)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL))
			return
		}

		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		if dump.Len() == 0 || bytes.Contains(bytes.ToUpper(dump.Bytes()), []byte("SECURITY LABEL")) {
			t.Fatal("dump must contain real user data and no fabricated assignments")
		}
		retained, err := b.DB.QueryContext(p.Ctx, pgSeclabelSelect)
		if err != nil { t.Fatal(err) }
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil || len(columns) != 5 { t.Fatal("retained descriptors", columns, err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		pgSeclabelIdentity(t, p, b, c)
		pgSeclabelEmpty(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL))
		pgSeclabelError(t, p, b, c, q, pgSeclabelAbsent())
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname::text FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='sl_sentinel'", []string{"relname"}, []string{"TEXT"}, nil))
		if retained.Next() || retained.Err() != nil { t.Fatal("retained empty cursor", retained.Err()) }
		if err := retained.Close(); err != nil { t.Fatal(err) }
		var names []string
		for i, typ := range columns {
			names = append(names, typ.Name())
			if typ.DatabaseTypeName() != pgSeclabelTypes[i] { t.Fatal("retained type", i, typ) }
		}
		if !reflect.DeepEqual(names, pgSeclabelColumns) { t.Fatal("retained names", names) }
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		pgSeclabelIdentity(t, p, b, c)
		pgSeclabelSentinel(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL))
		pgSeclabelError(t, p, b, c, q, pgSeclabelAbsent())

		// The existing Restore allow-list rejects utilities before Execute.
		// A staged table preceding the rejected command must never publish.
		bad := append([]byte(nil), dump.Bytes()...)
		bad = append(bad, []byte("\nCREATE TABLE public.sl_unpublished(v INT);\nSECURITY LABEL ON TABLE public.sl_sentinel IS 'secret';\n")...)
		err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad))
		if err == nil { t.Fatal("Restore accepted SECURITY LABEL") }
		want := pgSeclabelDiagnostic("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
		if got := fixture.ExtractDiagnostic(t, err); got != want { t.Fatalf("Restore diagnostic got=%#v want=%#v", got, want) }
		pgSeclabelIdentity(t, p, b, c)
		pgSeclabelSentinel(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname::text FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='sl_unpublished'", []string{"relname"}, []string{"TEXT"}, nil))
		pgSeclabelError(t, p, b, c, q, pgSeclabelAbsent())
	})
}
