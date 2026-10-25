package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConversionFeatureSmoke(t *testing.T) {
	conversionScenario(t, []string{"conv_smoke_hidden", "conv_smoke_moved"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE SCHEMA conv_smoke_hidden`, `CREATE SCHEMA conv_smoke_moved`, fixture.ConversionCreate("public.conv_smoke_plain", false))
		publicOID := conversionNamespaceOID(t, p, b, "public")
		destinationOID := conversionNamespaceOID(t, p, b, "conv_smoke_moved")
		conversionHideSeed(t, p, b, "conv_smoke_hidden")
		conversionError(t, p, b, conversionValue().Query, conversionMissingDefault(), false)
		conversionExec(t, p, b, fixture.ConversionCreate("public.conv_smoke_default", true))
		oid := conversionOID(t, p, b, "conv_smoke_default")
		conversionStep(t, p, b, conversionMetadata(oid, "conv_smoke_default", publicOID, true))
		conversionStep(t, p, b, conversionValue())
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		q := `SELECT convert_to($1::text,'LATIN1') AS v`
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, input := range []string{"é", "€", "é"} {
			s := fixture.Select(q, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}, input)
			if input == "€" {
				s = fixture.ErrorDiagnostic(q, conversionEuro(), input)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			want := `CREATE DEFAULT CONVERSION "public"."conv_smoke_default" FOR 'UTF8' TO 'LATIN1' FROM "pg_catalog"."utf8_to_iso8859_1";` + "\n"
			if !strings.Contains(dump.String(), want) {
				t.Fatal("exact smoke definition", dump.String())
			}
			fresh := fixture.UpdateReturningNativeDestination(t)
			if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.CatalogControlTuples(t, fresh, `SELECT convert_to('é','LATIN1')`, [][]any{{[]byte{0xe9}}})
		}
		conversionHeldExec(t, p, b, conn, `ALTER CONVERSION public.conv_smoke_default RENAME TO conv_smoke_renamed`)
		conversionHeldExec(t, p, b, conn, `ALTER CONVERSION public.conv_smoke_renamed SET SCHEMA conv_smoke_moved`)
		conversionStep(t, p, b, conversionMetadata(oid, "conv_smoke_renamed", destinationOID, true))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`SET search_path=conv_smoke_moved,public,pg_catalog`, 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xe9"}}, "é"))
		conversionHeldExec(t, p, b, conn, `DROP CONVERSION conv_smoke_moved.conv_smoke_renamed`)
		missing := fixture.ErrorDiagnostic(q, conversionMissingDefault(), "é")
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, missing)
	})
}
