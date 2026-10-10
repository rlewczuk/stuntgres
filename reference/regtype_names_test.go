package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceRegtypeNames(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'same'::regtype AS a,'regtype_b.same'::regtype AS b,'regtype_a.mood'::regtype AS c", "same", "regtype_b.same", "mood"))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT ('same'::regtype::oid='regtype_a.same'::regtype::oid) AS a,('regtype_a.same'::regtype::oid='regtype_b.same'::regtype::oid) AS b", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		regtypeBoth(t, p, conn, recorder, regtypeSelect(`SELECT '"Odd.Regtype"."a""b.dot"'::regtype AS a,'regtype_a."Select"'::regtype AS b,'regtype_a."23"'::regtype AS c,'regtype_a."-"'::regtype AS d`, `"Odd.Regtype"."a""b.dot"`, `"Select"`, `"23"`, `"-"`))
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT ' regtype_a . SAME '::regtype AS a,'regtype_a.same[]'::regtype AS b,'regtype_a._same'::regtype AS c,'regtype_a.mood[]'::regtype AS d", "same", "same[]", "same[]", "mood[]"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_b,regtype_a,public", 0))
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'same'::regtype AS a,'regtype_a.same'::regtype AS b", "same", "regtype_a.same"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO pg_catalog", 0))
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'regtype_a.same'::regtype AS a,'regtype_b.same'::regtype AS b", "regtype_a.same", "regtype_b.same"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec(`SET search_path TO "Odd.Regtype",pg_catalog`, 0))
		regtypeBoth(t, p, conn, recorder, regtypeSelect(`SELECT '"Odd.Regtype"."a""b.dot"'::regtype AS a`, `"a""b.dot"`))
		for _, name := range []string{"int4", "text", "integer", "numeric", "interval", "json", "bit"} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a."+regtypeIdentifier(name)+" AS pg_catalog.text", 0))
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_a,pg_catalog,public", 0))
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'int4'::regtype AS a,'integer'::regtype AS b,'pg_catalog.int4'::regtype AS c,'text'::regtype AS d,'pg_catalog.text'::regtype AS e", "int4", "integer", "integer", "text", "pg_catalog.text"))
		regtypeBoth(t, p, conn, recorder, regtypeSelect(`SELECT '"integer"'::regtype AS a,'regtype_a."integer"'::regtype AS b,'pg_catalog.numeric'::regtype AS c,'pg_catalog.interval'::regtype AS d,'pg_catalog.json'::regtype AS e,'pg_catalog.bit'::regtype AS f`, `"integer"`, `"integer"`, "numeric", "interval", "json", "bit"))
		// Identifier clipping is input semantics, not stored value truncation.
		long := "regtype_identifier_abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyz"
		clipped := long[:63]
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a."+regtypeIdentifier(clipped)+" AS pg_catalog.int4", 0))
		s := regtypeSelect("SELECT $1::regtype AS a", clipped)
		s.args = []any{"regtype_a." + long}
		regtypeBoth(t, p, conn, recorder, s)
		unicodeLong := strings.Repeat("é", 32)
		unicodeClipped := strings.Repeat("é", 31)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a."+regtypeIdentifier(unicodeClipped)+" AS pg_catalog.int4", 0))
		unicodeStep := regtypeSelect("SELECT $1::regtype AS a", regtypeIdentifier(unicodeClipped))
		unicodeStep.args = []any{"regtype_a." + regtypeIdentifier(unicodeLong)}
		regtypeBoth(t, p, conn, recorder, unicodeStep)
		clipping := func(input, source, clipped string) referenceDiagnostic {
			return referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + source + `" will be truncated to "` + clipped + `"`, Context: `invalid type name "` + input + "\"\nunnamed portal parameter $1 = '...'"}
		}
		asciiNotice := clipping("regtype_a."+long, long, clipped)
		unicodeNotice := clipping("regtype_a."+regtypeIdentifier(unicodeLong), unicodeLong, unicodeClipped)
		regtypeAssertNotices(t, *notices, []referenceDiagnostic{asciiNotice, asciiNotice, unicodeNotice, unicodeNotice})
	})
}

func TestReferenceRegtypeUserAndTemporaryPath(t *testing.T) {
	roleReferenceCleanup(t, []string{"regtype_reader"})
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, q := range []string{"CREATE ROLE regtype_reader", "CREATE SCHEMA regtype_reader AUTHORIZATION regtype_reader", "SET ROLE regtype_reader", "CREATE DOMAIN regtype_reader.mine AS integer", `SET search_path TO "$user",public`} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'mine'::regtype AS a,'regtype_reader.mine[]'::regtype AS b", "mine", "mine[]"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("RESET ROLE", 0))
		// Establish own temporary namespace using the existing supported sequence API,
		// not an unauthorized expansion of temporary table support.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TEMP SEQUENCE regtype_temp_sequence", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO pg_temp", 0))
		var namespace string
		if err := conn.QueryRowContext(p.ctx, "SELECT current_schema").Scan(&namespace); err != nil || !strings.HasPrefix(namespace, "pg_temp_") {
			t.Fatal("initialized temporary namespace", namespace, err)
		}
		if !backend.pg {
			// TEMP type DDL is an existing adjacent capability boundary; the
			// selected resolver does not enable that creator.
			q := "CREATE DOMAIN " + regtypeIdentifier(namespace) + ".regtype_temp_domain AS integer"
			referenceRunStep(t, p.ctx, conn, recorder, referenceImplicitError(q,
				controlsReferenceDiagnostic("0A000", "temporary schema creation is unsupported")))
			return
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN "+regtypeIdentifier(namespace)+".regtype_temp_domain AS integer", 0))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO public", 0))
		regtypeBoth(t, p, conn, recorder, regtypeSelect("SELECT 'regtype_temp_domain'::regtype AS a,'pg_temp.regtype_temp_domain[]'::regtype AS b", "regtype_temp_domain", "regtype_temp_domain[]"))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP DOMAIN pg_temp.regtype_temp_domain", 0))
	})
}
