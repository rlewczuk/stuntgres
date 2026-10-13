package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceConversionTemporary(t *testing.T) {
	conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Reuse the existing bounded physical-pool close and actual advisory exit
		// barrier; no polling delay or lease-return-as-session-end assumption.
		owner := extensionTemporaryOwner(t, p, b.PG)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner.conn, owner.recorder, s) }
		run(fixture.Select(`SELECT pg_advisory_lock(26070001)`, []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
		run(fixture.Exec(conversionCreate("pg_temp.conv_temporary", false), 0))
		run(fixture.Select(`SELECT conforencoding,contoencoding,condefault,pg_conversion_is_visible(oid) AS visible FROM pg_catalog.pg_conversion WHERE conname='conv_temporary'`, []string{"conforencoding", "contoencoding", "condefault", "visible"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{{int64(6), int64(8), false, false}}))
		// Explicit TEMP names are usable for utility name lookup despite implicit
		// conversion name/default paths excluding the temporary namespace.
		conversionHeldExec(t, p, b, owner.conn, `ALTER CONVERSION pg_temp.conv_temporary RENAME TO conv_temporary_renamed`)
		run(fixture.Select(`SELECT conname FROM pg_catalog.pg_conversion WHERE conname='conv_temporary_renamed'`, []string{"conname"}, []string{"NAME"}, [][]any{{"conv_temporary_renamed"}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "conv_temporary") {
				t.Fatal("temporary conversion serialized", dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT conname FROM pg_catalog.pg_conversion WHERE conname='conv_temporary_renamed'`, []string{"conname"}, []string{"NAME"}, [][]any{{"conv_temporary_renamed"}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select(`SELECT conname FROM pg_catalog.pg_conversion WHERE conname='conv_temporary_renamed'`, []string{"conname"}, []string{"NAME"}, [][]any{{"conv_temporary_renamed"}}))
		}
		if !owner.close(t) {
			t.FailNow()
		}
		extensionTempExitBarrier(t, p, peer, b.Recorder, 26070001)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, conversionCount("conv_temporary_renamed", 0))
	})
}
