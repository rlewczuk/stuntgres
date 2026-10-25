package reference_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConversionControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"conv_control_owner"})
	conversionScenario(t, []string{"conv_control_old", "conv_control_new", "conv_control_hidden", "conv_control_owner_schema"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var canonical bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &canonical); err != nil {
				t.Fatal(err)
			}
		}
		conversionExec(t, p, b, `CREATE SCHEMA conv_control_old`, `CREATE SCHEMA conv_control_new`, `CREATE SCHEMA conv_control_hidden`, fixture.ConversionCreate("public.conv_control_plain", false), fixture.ConversionCreate("public.conv_control_default", true), fixture.ConversionCreate("conv_control_old.conv_control_a", true), fixture.ConversionCreate("conv_control_new.conv_control_b", true), `ALTER CONVERSION conv_control_old.conv_control_a SET SCHEMA conv_control_new`, `ALTER CONVERSION conv_control_new.conv_control_a RENAME TO conv_control_renamed`)
		conversionHideSeed(t, p, b, "conv_control_hidden")
		conversionExec(t, p, b, `ALTER CONVERSION conv_control_hidden.utf8_to_iso_8859_1 RENAME TO conv_control_seed`)
		conversionExec(t, p, b, `CREATE USER conv_control_owner`, `CREATE SCHEMA conv_control_owner_schema AUTHORIZATION conv_control_owner`, fixture.ConversionCreate("conv_control_owner_schema.conv_control_owned", false), `ALTER CONVERSION conv_control_owner_schema.conv_control_owned OWNER TO conv_control_owner`)
		ownerMetadata := fixture.Select(`SELECT c.conowner=r.oid AS owned FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_roles r ON r.rolname='conv_control_owner' WHERE c.conname='conv_control_owned'`, []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		conversionStep(t, p, b, ownerMetadata)
		metadata := fixture.Select(`SELECT conname,condefault,conproc::oid AS handler FROM pg_catalog.pg_conversion WHERE conname='conv_control_plain' OR conname='conv_control_b' OR conname='conv_control_renamed' OR conname='conv_control_seed' ORDER BY conname`, []string{"conname", "condefault", "handler"}, []string{"NAME", "BOOL", "OID"}, [][]any{{"conv_control_b", true, int64(4375)}, {"conv_control_plain", false, int64(4375)}, {"conv_control_renamed", true, int64(4375)}, {"conv_control_seed", true, int64(4375)}})
		conversionStep(t, p, b, metadata)
		conversionStep(t, p, b, conversionValue())
		// PostgreSQL has no native control API. Its phase above independently
		// validates the SQL metadata; the actual native dump is replayed below.
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		plain := `CREATE CONVERSION "public"."conv_control_plain" FOR 'UTF8' TO 'LATIN1' FROM "pg_catalog"."utf8_to_iso8859_1";` + "\n"
		if !strings.Contains(dump.String(), plain) {
			t.Fatal("missing exact nondefault definition", dump.String())
		}
		definition := `CREATE DEFAULT CONVERSION "public"."conv_control_default" FOR 'UTF8' TO 'LATIN1' FROM "pg_catalog"."utf8_to_iso8859_1";` + "\n"
		if !strings.Contains(dump.String(), definition) {
			t.Fatal("missing exact default definition", dump.String())
		}
		if !strings.Contains(dump.String(), "CREATE DEFAULT CONVERSION ") || !strings.Contains(dump.String(), "ALTER CONVERSION ") {
			t.Fatal("missing duplicate-default/seed metadata definitions", dump.String())
		}
		for _, forbidden := range []string{"$libdir", "probin", "pg_conversion.dat"} {
			if strings.Contains(dump.String(), forbidden) {
				t.Fatal("serialized provider data", forbidden)
			}
		}
		rows, err := b.DB.QueryContext(p.Ctx, metadata.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		var resetDump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &resetDump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical.Bytes(), resetDump.Bytes()) {
			t.Fatal("canonical seed-only legacy dump changed", canonical.String(), resetDump.String())
		}
		conversionStep(t, p, b, conversionCount("conv_control_plain", 0))
		conversionStep(t, p, b, conversionCount("utf8_to_iso_8859_1", 1))
		conversionStep(t, p, b, conversionValue())
		fixture.UpdateCoreRetained(t, false, rows, metadata.Columns, metadata.Types, metadata.Rows, false)
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		conversionStep(t, p, b, metadata)
		conversionStep(t, p, b, ownerMetadata)
		conversionStep(t, p, b, conversionValue())
		var redump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("conversion dump is not deterministic after restore", dump.String(), redump.String())
		}
		fresh := fixture.UpdateReturningNativeDestination(t)
		// Allocate target identities and live TEMP before importing; neither can
		// be silently stolen by imported catalog addresses.
		fixture.CatalogControlExec(t, fresh, `CREATE TABLE conv_import_collision(v INT)`)
		temp := fixture.SequenceCoreConn(t, p.Ctx, fresh)
		fixture.CatalogControlExec(t, fresh, `CREATE TABLE conv_control_sentinel(v INT)`)
		if _, err = temp.ExecContext(p.Ctx, `CREATE TEMP TABLE conv_control_temp(v INT)`); err != nil {
			t.Fatal(err)
		}
		if _, err = temp.ExecContext(p.Ctx, `INSERT INTO conv_control_temp VALUES(7)`); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("missing captured owner replay succeeded")
		}
		fixture.CatalogControlTuples(t, fresh, `SELECT count(*)::bigint FROM conv_control_sentinel`, [][]any{{int64(0)}})
		fixture.CatalogControlExec(t, fresh, `CREATE USER conv_control_owner`)
		if err = stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("independent Driver conversion replay", err)
		}
		fixture.CatalogControlTuples(t, fresh, metadata.Query, metadata.Rows)
		fixture.CatalogControlTuples(t, fresh, ownerMetadata.Query, ownerMetadata.Rows)
		tempValue := fixture.Select(`SELECT v FROM conv_control_temp`, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		tempRows, tempErr := temp.QueryContext(p.Ctx, tempValue.Query)
		fixture.DeleteReferenceConsume(t, false, tempRows, tempErr, tempValue)
		conversionStep(t, p, b, fixture.Exec(`CREATE TABLE conv_control_sentinel(v INT)`, 0))
		conversionStep(t, p, b, fixture.Exec(`INSERT INTO conv_control_sentinel VALUES(99)`, 1))
		for _, input := range []string{dump.String() + "\nCREATE CONVERSION conv_late FOR 'bad' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1;\n", dump.String() + "\nCREATE CONVERSION conv_late FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.conv_unavailable;\n"} {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(input)); err == nil {
				t.Fatal("invalid late conversion restore succeeded")
			}
			conversionStep(t, p, b, fixture.Select(`SELECT v FROM conv_control_sentinel`, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			conversionStep(t, p, b, metadata)
		}
		readerErr := errors.New("conversion reader failure")
		if err = stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: readerErr}); !errors.Is(err, readerErr) {
			t.Fatal("restore reader error", err)
		}
		conversionStep(t, p, b, fixture.Select(`SELECT v FROM conv_control_sentinel`, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		if p.Postgres != nil {
			pg := fixture.SequenceCoreConn(t, p.Ctx, p.Postgres)
			// The native phase runs before the independent PostgreSQL setup.
			// Supply only the portable owner principal, not provider definitions.
			if _, err = pg.ExecContext(p.Ctx, `CREATE USER conv_control_owner`); err != nil {
				t.Fatal(err)
			}
			if _, err = pg.ExecContext(p.Ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer pg.ExecContext(p.Ctx, `ROLLBACK`)
			if _, err = pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual native conversion dump PostgreSQL replay", err, dump.String())
			}
			fixture.OidReferenceRows(t, p.Ctx, fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}, pg, metadata)
			fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, conversionValue())
			if _, err = pg.ExecContext(p.Ctx, `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if _, err = pg.ExecContext(p.Ctx, `DROP USER conv_control_owner`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
