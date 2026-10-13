package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConversionMetadata(t *testing.T) {
	conversionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Empty rows still expose all eight exact catalog types, including REGPROC.
		conversionStep(t, p, b, fixture.Select(`SELECT oid,conname,connamespace,conowner,conforencoding,contoencoding,conproc,condefault FROM pg_catalog.pg_conversion WHERE false`, []string{"oid", "conname", "connamespace", "conowner", "conforencoding", "contoencoding", "conproc", "condefault"}, []string{"OID", "NAME", "OID", "OID", "INT4", "INT4", "REGPROC", "BOOL"}, [][]any{}))
		seeds := make([][]any, 0, len(conversionReleasedSeeds))
		for _, s := range conversionReleasedSeeds {
			seeds = append(seeds, []any{s.oid, s.name, int64(11), s.source, s.destination, s.handler, s.routine, true, true})
		}
		s := fixture.Select(`SELECT c.oid::bigint AS oid,c.conname,c.connamespace::bigint AS namespace,c.conforencoding,c.contoencoding,c.conproc::oid::bigint AS handler,p.proname,c.condefault,c.conowner=p.proowner AS same_owner FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_proc p ON p.oid=c.conproc::oid ORDER BY c.oid`, []string{"oid", "conname", "namespace", "conforencoding", "contoencoding", "handler", "proname", "condefault", "same_owner"}, []string{"INT8", "NAME", "INT8", "INT4", "INT4", "INT8", "NAME", "BOOL", "BOOL"}, seeds)
		conversionStep(t, p, b, s)
		conversionStep(t, p, b, fixture.Prepared(s))
		handlers := make([][]any, 0, len(conversionReleasedHandlers))
		for _, h := range conversionReleasedHandlers {
			handlers = append(handlers, []any{h.oid, h.name, int64(11), int64(6), int64(23), "i", true, "s"})
		}
		conversionStep(t, p, b, fixture.Select(`SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,pronargs,prorettype::bigint AS result,provolatile,proisstrict,proparallel FROM pg_catalog.pg_proc WHERE oid>=4302 AND oid<=4387 AND oid<>4350 AND oid<>4351 ORDER BY oid`, []string{"oid", "proname", "namespace", "pronargs", "result", "provolatile", "proisstrict", "proparallel"}, []string{"INT8", "NAME", "INT8", "INT2", "INT8", "CHAR", "BOOL", "CHAR"}, handlers))
		// Additional oracle metadata beyond the native bounded nine-column
		// projection confirms the actual released ABI; shared CREATE diagnostics
		// and U1 validate native identity without broadening that projection.
		if b.PG {
			signatures := make([][]any, 0, len(conversionReleasedHandlers))
			for _, h := range conversionReleasedHandlers {
				signatures = append(signatures, []any{h.oid, "23 23 2275 2281 23 16"})
			}
			conversionStep(t, p, b, fixture.Select(`SELECT oid::bigint AS oid,proargtypes::text AS args FROM pg_catalog.pg_proc WHERE oid>=4302 AND oid<=4387 AND oid<>4350 AND oid<>4351 ORDER BY oid`, []string{"oid", "args"}, []string{"INT8", "TEXT"}, signatures))
		}
		publicOID := conversionNamespaceOID(t, p, b, "public")
		conversionExec(t, p, b, conversionCreate("conv_meta", false))
		oid := conversionOID(t, p, b, "conv_meta")
		conversionStep(t, p, b, conversionMetadata(oid, "conv_meta", publicOID, false))
		rows, err := b.DB.QueryContext(p.Ctx, `SELECT conproc,conproc::oid::bigint AS oid,conproc::text AS text FROM pg_catalog.pg_conversion WHERE conname='conv_meta'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		conversionExec(t, p, b, `ALTER CONVERSION conv_meta RENAME TO conv_meta_renamed`, `DROP CONVERSION conv_meta_renamed`)
		fixture.UpdateCoreRetained(t, b.PG, rows, []string{"conproc", "oid", "text"}, []string{"REGPROC", "INT8", "TEXT"}, [][]any{{"utf8_to_iso8859_1", int64(4375), "utf8_to_iso8859_1"}}, false)
		// pg_catalog is implicitly searchable even when omitted; explicit path
		// placement does not turn a catalog-derived routine identity into an OID.
		b.DB.SetMaxOpenConns(1)
		conversionExec(t, p, b, `SET search_path=public`)
		conversionStep(t, p, b, fixture.Select(`SELECT conproc,conproc::text AS text,conproc::oid AS oid FROM pg_catalog.pg_conversion WHERE conname='utf8_to_iso_8859_1'`, []string{"conproc", "text", "oid"}, []string{"REGPROC", "TEXT", "OID"}, [][]any{{"utf8_to_iso8859_1", "utf8_to_iso8859_1", int64(4375)}}))
		// Real oracle setup only: arbitrary SQL routine creation is not a native
		// capability. U3 covers the matching native procedure/output seam.
		if b.PG {
			conversionExec(t, p, b, `CREATE FUNCTION public.utf8_to_iso8859_1(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'`, `SET search_path=public,pg_catalog`)
			conversionStep(t, p, b, fixture.Select(`SELECT conproc,conproc::text AS text,conproc::oid AS oid FROM pg_catalog.pg_conversion WHERE conname='utf8_to_iso_8859_1'`, []string{"conproc", "text", "oid"}, []string{"REGPROC", "TEXT", "OID"}, [][]any{{"pg_catalog.utf8_to_iso8859_1", "pg_catalog.utf8_to_iso8859_1", int64(4375)}}))
			conversionExec(t, p, b, `DROP FUNCTION public.utf8_to_iso8859_1(integer)`)
		}
		// Mutation remains a deliberate native boundary. PostgreSQL superusers
		// may mutate system catalogs, so no invented permission error is shared.
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(`UPDATE pg_catalog.pg_conversion SET condefault=condefault WHERE false`, conversionDiagnostic("0A000", "catalog source mutation is unsupported"))
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				conversionStep(t, p, b, step)
			}
		}
	})
}
