package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionDefaultsAndLookup(t *testing.T) {
	conversionScenario(t, []string{"conv_hidden", "conv_path"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		b.DB.SetMaxOpenConns(1)
		conversionExec(t, p, b, `CREATE SCHEMA conv_hidden`, `CREATE SCHEMA conv_path`, `SET search_path=public,pg_catalog`)
		conversionHideSeed(t, p, b, "conv_hidden")
		conversionError(t, p, b, conversionValue().Query, conversionMissingDefault(), false)
		conversionExec(t, p, b, fixture.ConversionCreate("public.conv_nondefault", false), fixture.ConversionCreate("public.conv_nondefault_2", false))
		conversionError(t, p, b, conversionValue().Query, conversionMissingDefault(), false)
		conversionExec(t, p, b, fixture.ConversionCreate("public.conv_default", true))
		conversionStep(t, p, b, conversionValue())
		conversionStep(t, p, b, fixture.Prepared(conversionValue()))
		conversionError(t, p, b, fixture.ConversionCreate("public.conv_duplicate", true), conversionDiagnostic("42710", `default conversion for UTF8 to LATIN1 already exists`), false)
		conversionExec(t, p, b, fixture.ConversionCreate("conv_path.conv_default", true), `CREATE DEFAULT CONVERSION public.conv_reverse FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8`)
		conversionExec(t, p, b, `ALTER CONVERSION public.conv_default RENAME TO conv_renamed`)
		conversionStep(t, p, b, conversionValue())
		conversionExec(t, p, b, `DROP CONVERSION public.conv_renamed`)
		conversionError(t, p, b, conversionValue().Query, conversionMissingDefault(), false)
		// Empty/NULL/equal conversion fast paths still work without a default.
		conversionStep(t, p, b, fixture.Select(`SELECT convert_to('','LATIN1') AS empty,convert_to(NULL::text,'LATIN1') AS absent,convert(decode('c3a9','hex'),'UTF8','UTF8') AS same`, []string{"empty", "absent", "same"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{"", nil, "é"}}))
		conversionExec(t, p, b, `SET search_path=conv_path,public,pg_catalog`)
		conversionStep(t, p, b, conversionValue())
		conversionStep(t, p, b, fixture.Select(`SELECT pg_conversion_is_visible(oid) AS visible FROM pg_catalog.pg_conversion WHERE conname='conv_default'`, []string{"visible"}, []string{"BOOL"}, [][]any{{true}}))
		conversionExec(t, p, b, `SET search_path=conv_hidden,public,pg_catalog`)
		conversionStep(t, p, b, conversionValue())
		conversionExec(t, p, b, `ALTER CONVERSION conv_hidden.utf8_to_iso_8859_1 RENAME TO conv_seed_renamed`)
		conversionStep(t, p, b, conversionValue())
		conversionExec(t, p, b, `SET search_path=public,pg_catalog`)
		conversionError(t, p, b, conversionValue().Query, conversionMissingDefault(), false)
	})
}

func TestReferenceConversionDefaultSchemaMove(t *testing.T) {
	conversionScenario(t, conversionSchemaNames("conv_move"), func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		b.DB.SetMaxOpenConns(1)
		conversionExec(t, p, b, `CREATE SCHEMA conv_move_old`, `CREATE SCHEMA conv_move_new`, fixture.ConversionCreate("conv_move_old.a", true), fixture.ConversionCreate("conv_move_new.b", true))
		destinationOID := conversionNamespaceOID(t, p, b, "conv_move_new")
		oid := conversionOID(t, p, b, "a")
		conversionStep(t, p, b, fixture.Prepared(fixture.Exec(`ALTER CONVERSION conv_move_old.a SET SCHEMA conv_move_new`, 0)))
		conversionStep(t, p, b, conversionMetadata(oid, "a", destinationOID, true))
		conversionStep(t, p, b, fixture.Select(`SELECT conname,condefault FROM pg_catalog.pg_conversion WHERE connamespace=$1 ORDER BY conname`, []string{"conname", "condefault"}, []string{"NAME", "BOOL"}, [][]any{{"a", true}, {"b", true}}, destinationOID))
		conversionError(t, p, b, fixture.ConversionCreate("conv_move_new.c", true), conversionDiagnostic("42710", `default conversion for UTF8 to LATIN1 already exists`), false)
		conversionExec(t, p, b, `ALTER CONVERSION conv_move_new.a RENAME TO renamed`, `SET search_path=conv_move_new,pg_catalog`)
		conversionStep(t, p, b, conversionValue())
		conversionExec(t, p, b, `DROP CONVERSION conv_move_new.b`)
		conversionStep(t, p, b, conversionValue())
		conversionStep(t, p, b, conversionMetadata(oid, "renamed", destinationOID, true))
		conversionExec(t, p, b, fixture.ConversionCreate("conv_move_old.renamed", false))
		conversionError(t, p, b, `ALTER CONVERSION conv_move_old.renamed SET SCHEMA conv_move_new`, conversionDiagnostic("42710", `conversion "renamed" already exists in schema "conv_move_new"`), false)
	})
}
