package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionLifecycle(t *testing.T) {
	conversionScenario(t, []string{"conv_life"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE SCHEMA conv_life`)
		publicOID := conversionNamespaceOID(t, p, b, "public")
		destinationOID := conversionNamespaceOID(t, p, b, "conv_life")
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		conversionHeldExec(t, p, b, conn, `CREATE CONVERSION public.Conv_Life FOR 'u_t-f8' TO 'ISO-8859-1' FROM pg_catalog.utf8_to_iso8859_1`)
		oid := conversionOID(t, p, b, "conv_life")
		conversionStep(t, p, b, conversionMetadata(oid, "conv_life", publicOID, false))
		conversionStep(t, p, b, fixture.Select(`SELECT c.conowner=r.oid AS owned FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_roles r ON r.rolname=current_user WHERE c.oid=$1`, []string{"owned"}, []string{"BOOL"}, [][]any{{true}}, oid))
		q := `SELECT conname FROM pg_catalog.pg_conversion WHERE oid=$1`
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		runHeld := func(name string) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"conname"}, []string{"NAME"}, [][]any{{name}}, oid))
		}
		runHeld("conv_life")
		conversionHeldExec(t, p, b, conn, `ALTER CONVERSION public.conv_life RENAME TO "Conv Renamed"`)
		runHeld("Conv Renamed")
		conversionHeldExec(t, p, b, conn, `ALTER CONVERSION public."Conv Renamed" SET SCHEMA conv_life`)
		conversionHeldExec(t, p, b, conn, `ALTER CONVERSION conv_life."Conv Renamed" OWNER TO CURRENT_USER`)
		conversionStep(t, p, b, conversionMetadata(oid, "Conv Renamed", destinationOID, false))
		conversionError(t, p, b, `DROP CONVERSION public.conv_life`, conversionDiagnostic("42704", `conversion "public.conv_life" does not exist`), false)
		conversionExec(t, p, b, fixture.ConversionCreate("conv_batch_a", false), fixture.ConversionCreate("conv_batch_b", false))
		conversionError(t, p, b, `DROP CONVERSION conv_batch_a,conv_batch_missing`, conversionDiagnostic("42704", `conversion "conv_batch_missing" does not exist`), false)
		conversionStep(t, p, b, conversionCount("conv_batch_a", 1))
		conversionExec(t, p, b, `DROP CONVERSION conv_batch_a,conv_batch_b RESTRICT`, `DROP CONVERSION conv_life."Conv Renamed" CASCADE`)
		conversionStep(t, p, b, conversionCount("Conv Renamed", 0))
		conversionNotice(t, p, b, `DROP CONVERSION IF EXISTS conv_life_absent`, `conversion "conv_life_absent" does not exist, skipping`)
		conversionNotice(t, p, b, `DROP CONVERSION IF EXISTS conv_missing_schema.absent`, `schema "conv_missing_schema" does not exist, skipping`)
		longName := strings.Repeat("x", 63)
		conversionExec(t, p, b, fixture.ConversionCreate(conversionQuote(longName), false))
		conversionStep(t, p, b, conversionCount(longName, 1))
		conversionExec(t, p, b, "DROP CONVERSION "+conversionQuote(longName))
		conversionExec(t, p, b, `CREATE CONVERSION conv_reverse FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.iso8859_1_to_utf8`)
		conversionStep(t, p, b, fixture.Select(`SELECT conforencoding,contoencoding,conproc::oid AS handler FROM pg_catalog.pg_conversion WHERE conname='conv_reverse'`, []string{"conforencoding", "contoencoding", "handler"}, []string{"INT4", "INT4", "OID"}, [][]any{{int64(8), int64(6), int64(4374)}}))
		conversionExec(t, p, b, `DROP CONVERSION conv_reverse`)
	})
}

func TestReferenceConversionDiagnostics(t *testing.T) {
	conversionScenario(t, []string{"conv_diag"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conversionExec(t, p, b, `CREATE SCHEMA conv_diag`, fixture.ConversionCreate("conv_diag.existing", true))
		cases := []struct{ query, state, message string }{
			{`CREATE CONVERSION conv_bad FOR 'bad' TO 'also_bad' FROM absent`, "42704", `source encoding "bad" does not exist`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'bad' FROM absent`, "42704", `destination encoding "bad" does not exist`},
			{`CREATE CONVERSION conv_bad FOR 'SQL_ASCII' TO 'UTF8' FROM absent`, "42P17", `encoding conversion to or from "SQL_ASCII" is not supported`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'SQL_ASCII' FROM absent`, "42P17", `encoding conversion to or from "SQL_ASCII" is not supported`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'LATIN1' FROM lower`, "42883", `function lower(integer, integer, cstring, internal, integer, boolean) does not exist`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'LATIN1' FROM convert`, "42883", `function convert(integer, integer, cstring, internal, integer, boolean) does not exist`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.conv_absent`, "42883", `function pg_catalog.conv_absent(integer, integer, cstring, internal, integer, boolean) does not exist`},
			{`CREATE CONVERSION conv_bad FOR 'LATIN1' TO 'UTF8' FROM pg_catalog.utf8_to_iso8859_1`, "XX000", `expected source encoding "UTF8", but got "LATIN1"`},
			{`CREATE CONVERSION conv_bad FOR 'UTF8' TO 'LATIN2' FROM pg_catalog.utf8_to_iso8859_1`, "XX000", `expected destination encoding "LATIN1", but got "LATIN2"`},
			{`CREATE CONVERSION conv_diag.existing FOR 'bad' TO 'LATIN1' FROM absent`, "42704", `source encoding "bad" does not exist`},
			{fixture.ConversionCreate("conv_diag.existing", true), "42710", `conversion "existing" already exists`},
			{fixture.ConversionCreate("conv_diag.another", true), "42710", `default conversion for UTF8 to LATIN1 already exists`},
			{`ALTER CONVERSION conv_diag.absent RENAME TO other`, "42704", `conversion "conv_diag.absent" does not exist`},
			{`ALTER CONVERSION conv_diag.existing SET SCHEMA conv_missing_schema`, "3F000", `schema "conv_missing_schema" does not exist`},
			{fixture.ConversionCreate("conv_missing_schema.c", false), "3F000", `schema "conv_missing_schema" does not exist`},
			{`DROP CONVERSION pg_catalog.utf8_to_iso_8859_1`, "2BP01", `cannot drop conversion utf8_to_iso_8859_1 because it is required by the database system`},
		}
		for _, c := range cases {
			t.Run(c.query, func(t *testing.T) { conversionError(t, p, b, c.query, conversionDiagnostic(c.state, c.message), false) })
		}
		for _, source := range []bool{true, false} {
			long := "UTF8" + strings.Repeat("-", 60)
			q := `CREATE CONVERSION conv_bad FOR '` + long + `' TO 'LATIN1' FROM absent`
			message := `source encoding "` + long + `" does not exist`
			if !source {
				q = `CREATE CONVERSION conv_bad FOR 'UTF8' TO '` + long + `' FROM absent`
				message = `destination encoding "` + long + `" does not exist`
			}
			conversionError(t, p, b, q, conversionDiagnostic("42704", message), false)
		}
		conversionExec(t, p, b, fixture.ConversionCreate("conv_diag.collision", false))
		conversionError(t, p, b, `ALTER CONVERSION conv_diag.existing RENAME TO collision`, conversionDiagnostic("42710", `conversion "collision" already exists in schema "conv_diag"`), false)
		// Syntax positions are independent byte offsets of the rejected token.
		grammar := []struct{ query, token string }{
			{`CREATE OR REPLACE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM f`, "CONVERSION"},
			{`CREATE CONVERSION IF NOT EXISTS c FOR 'UTF8' TO 'LATIN1' FROM f`, "NOT"},
			{`CREATE CONVERSION c FOR UTF8 TO 'LATIN1' FROM f`, "UTF8"},
			{`CREATE CONVERSION c FOR $1 TO 'LATIN1' FROM f`, "$1"},
			{`ALTER CONVERSION IF EXISTS c RENAME TO d`, "EXISTS"},
			{`ALTER CONVERSION c SET DEFAULT`, "DEFAULT"},
			{`DROP DEFAULT CONVERSION c`, "DEFAULT"},
		}
		for _, c := range grammar {
			d := conversionDiagnostic("42601", `syntax error at or near "`+c.token+`"`)
			d.Position = fixture.Position(strings.Index(c.query, c.token) + 1)
			conversionError(t, p, b, c.query, d, true)
		}
	})
}
