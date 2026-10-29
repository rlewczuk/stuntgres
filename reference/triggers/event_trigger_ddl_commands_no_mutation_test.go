package reference_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// A successful returned address is a command receipt even when the owner has
// nothing to replace. Each case checks independently specified object facts
// before and after execution, not merely equality between the two backends.
func TestReferenceEventTriggerDDLCommandsNoMutation(t *testing.T) {
	for _, tc := range []struct {
		name, setup, query, tag, class, kind, identity string
		catalog, predicate, facts                      string
		refresh                                        bool
	}{
		{
			name:  "sequence_owner",
			setup: "CREATE SEQUENCE et_dc_nm_seq AS bigint INCREMENT BY 3 START WITH 7 MINVALUE 1 MAXVALUE 100 CACHE 1 NO CYCLE",
			query: "ALTER SEQUENCE et_dc_nm_seq OWNER TO CURRENT_USER",
			tag:   "ALTER SEQUENCE", class: "1259", kind: "sequence", identity: "public.et_dc_nm_seq",
			catalog: "pg_class", predicate: "c.relname='et_dc_nm_seq' AND c.relnamespace='public'::regnamespace",
			facts: "SELECT c.relname='et_dc_nm_seq' AND c.relnamespace='public'::regnamespace AND c.relowner=r.oid AND c.relkind='S' AND s.seqtypid=20 AND s.seqstart=7 AND s.seqincrement=3 AND s.seqmin=1 AND s.seqmax=100 AND s.seqcache=1 AND NOT s.seqcycle AS unchanged FROM pg_class c JOIN pg_roles r ON r.rolname=current_user JOIN pg_sequence s ON s.seqrelid=c.oid WHERE c.relname='et_dc_nm_seq' AND c.relnamespace='public'::regnamespace",
		},
		{
			name: "collation_owner", setup: "CREATE COLLATION et_dc_nm_coll FROM pg_catalog.\"C\"",
			query: "ALTER COLLATION et_dc_nm_coll OWNER TO CURRENT_USER",
			tag:   "ALTER COLLATION", class: "3456", kind: "collation", identity: "public.et_dc_nm_coll",
			catalog: "pg_collation", predicate: "c.collname='et_dc_nm_coll' AND c.collnamespace='public'::regnamespace",
			facts: ddlCommandsNoMutationCollationFacts,
		},
		{
			name: "collation_schema", setup: "CREATE COLLATION et_dc_nm_coll FROM pg_catalog.\"C\"",
			query: "ALTER COLLATION et_dc_nm_coll SET SCHEMA public",
			tag:   "ALTER COLLATION", class: "3456", kind: "collation", identity: "public.et_dc_nm_coll",
			catalog: "pg_collation", predicate: "c.collname='et_dc_nm_coll' AND c.collnamespace='public'::regnamespace",
			facts: ddlCommandsNoMutationCollationFacts,
		},
		{
			name: "collation_refresh", setup: "CREATE COLLATION et_dc_nm_coll FROM pg_catalog.\"C\"",
			query: "ALTER COLLATION et_dc_nm_coll REFRESH VERSION",
			tag:   "ALTER COLLATION", class: "3456", kind: "collation", identity: "public.et_dc_nm_coll",
			catalog: "pg_collation", predicate: "c.collname='et_dc_nm_coll' AND c.collnamespace='public'::regnamespace",
			facts: ddlCommandsNoMutationCollationFacts, refresh: true,
		},
		{
			name: "conversion_owner", setup: "CREATE CONVERSION et_dc_nm_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1",
			query: "ALTER CONVERSION et_dc_nm_conv OWNER TO CURRENT_USER",
			tag:   "ALTER CONVERSION", class: "2607", kind: "conversion", identity: "public.et_dc_nm_conv",
			catalog: "pg_conversion", predicate: "c.conname='et_dc_nm_conv' AND c.connamespace='public'::regnamespace",
			facts: ddlCommandsNoMutationConversionFacts,
		},
		{
			name: "conversion_schema", setup: "CREATE CONVERSION et_dc_nm_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1",
			query: "ALTER CONVERSION et_dc_nm_conv SET SCHEMA public",
			tag:   "ALTER CONVERSION", class: "2607", kind: "conversion", identity: "public.et_dc_nm_conv",
			catalog: "pg_conversion", predicate: "c.conname='et_dc_nm_conv' AND c.connamespace='public'::regnamespace",
			facts: ddlCommandsNoMutationConversionFacts,
		},
		{
			name: "operator_owner", setup: "CREATE OPERATOR public.=== (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4eq)",
			query: "ALTER OPERATOR public.===(integer,integer) OWNER TO CURRENT_USER",
			tag:   "ALTER OPERATOR", class: "2617", kind: "operator", identity: "public.===(integer,integer)",
			catalog: "pg_operator", predicate: "c.oprname='===' AND c.oprnamespace='public'::regnamespace AND c.oprleft=23 AND c.oprright=23",
			facts: "SELECT c.oprname='===' AND c.oprnamespace='public'::regnamespace AND c.oprowner=r.oid AND c.oprleft=23 AND c.oprright=23 AND c.oprresult=16 AND c.oprcode='pg_catalog.int4eq'::regproc AND c.oprcom=0 AND c.oprnegate=0 AND NOT c.oprcanmerge AND NOT c.oprcanhash AS unchanged FROM pg_operator c JOIN pg_roles r ON r.rolname=current_user WHERE c.oprname='===' AND c.oprnamespace='public'::regnamespace AND c.oprleft=23 AND c.oprright=23",
		},
		{
			name: "template_schema", setup: "CREATE TEXT SEARCH TEMPLATE et_dc_nm_template(INIT=pg_catalog.dsimple_init,LEXIZE=pg_catalog.dsimple_lexize)",
			query: "ALTER TEXT SEARCH TEMPLATE et_dc_nm_template SET SCHEMA public",
			tag:   "ALTER TEXT SEARCH TEMPLATE", class: "3764", kind: "text search template", identity: "public.et_dc_nm_template",
			catalog: "pg_ts_template", predicate: "c.tmplname='et_dc_nm_template' AND c.tmplnamespace='public'::regnamespace",
			facts: "SELECT tmplname='et_dc_nm_template' AND tmplnamespace='public'::regnamespace AND tmplinit='pg_catalog.dsimple_init'::regproc AND tmpllexize='pg_catalog.dsimple_lexize'::regproc AS unchanged FROM pg_ts_template WHERE tmplname='et_dc_nm_template' AND tmplnamespace='public'::regnamespace",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ddlCommandScenario(t, func(d ddlCommandCase) {
				var notices []fixture.Diagnostic
				pool, c, recorder := fixture.NoticeReferencePool(t, d.p, d.b.PG, &notices)
				defer pool.Close()
				defer c.Close()
				defer c.ExecContext(context.WithoutCancel(d.p.Ctx), "ROLLBACK")
				d.c, d.b.Recorder = c, recorder
				d.exec(tc.setup)
				var rawOID any
				if err := c.QueryRowContext(d.p.Ctx, "SELECT c.oid FROM "+tc.catalog+" c WHERE "+tc.predicate).Scan(&rawOID); err != nil {
					t.Fatal(err)
				}
				oid := fixture.OidReferenceValue(t, d.b.PG, "OID", rawOID).(int64)
				if oid == 0 {
					t.Fatal("resolved target has no OID")
				}
				state := fixture.Select(tc.facts, []string{"unchanged"}, []string{"BOOL"}, [][]any{{true}})
				d.run(state)
				d.install("'" + tc.tag + "'")
				for _, prepared := range []bool{false, true} {
					d.exec("BEGIN")
					notices = nil
					s := fixture.Exec(tc.query, 0)
					s.Prepared = prepared
					d.run(s)
					if tc.refresh {
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "version has not changed"}
						if got := fixture.NoticeWitnesses(t, d.b.PG, notices, want); !reflect.DeepEqual(got, []fixture.Diagnostic{want}) {
							t.Fatalf("notices=%#v", got)
						}
					} else {
						for _, notice := range notices {
							if !d.b.PG {
								t.Fatalf("unexpected notice: %#v", notice)
							}
							fixture.NoticesInternalDebug(t, notice)
						}
					}
					d.rows(ddlCommandRows(tc.class, tc.tag, tc.kind, "public", tc.identity))
					address := fixture.Select("SELECT pass,objid FROM et_dc_audit ORDER BY pass", []string{"pass", "objid"}, []string{"INT4", "OID"}, [][]any{{int64(1), oid}, {int64(2), oid}})
					d.run(address)
					d.run(fixture.Prepared(address))
					d.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN "+tc.catalog+" c ON a.objid=c.oid WHERE a.classid="+tc.class+" AND "+tc.predicate, 2))
					d.run(state)
					d.run(fixture.Prepared(state))
					if tc.kind == "sequence" {
						d.run(fixture.Select("SELECT last_value,is_called FROM et_dc_nm_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(7), false}}))
					}
					d.exec("ROLLBACK")
					d.rows(nil)
					d.run(state)
				}
			})
		})
	}
}

const ddlCommandsNoMutationCollationFacts = "SELECT c.collname='et_dc_nm_coll' AND c.collnamespace='public'::regnamespace AND c.collowner=r.oid AND c.collprovider='c' AND c.collencoding=-1 AND c.collisdeterministic AND c.collcollate='C' AND c.collctype='C' AND c.colllocale IS NULL AND c.collicurules IS NULL AND c.collversion IS NULL AS unchanged FROM pg_collation c JOIN pg_roles r ON r.rolname=current_user WHERE c.collname='et_dc_nm_coll' AND c.collnamespace='public'::regnamespace"

const ddlCommandsNoMutationConversionFacts = "SELECT c.conname='et_dc_nm_conv' AND c.connamespace='public'::regnamespace AND c.conowner=r.oid AND c.conforencoding=6 AND c.contoencoding=8 AND c.conproc='pg_catalog.utf8_to_iso8859_1'::regproc AND NOT c.condefault AS unchanged FROM pg_conversion c JOIN pg_roles r ON r.rolname=current_user WHERE c.conname='et_dc_nm_conv' AND c.connamespace='public'::regnamespace"
