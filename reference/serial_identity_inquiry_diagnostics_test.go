package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lib/pq"
)

func serialIdentityMissingFunction(signature string, position int) referenceDiagnostic {
	w := controlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	w.Position = referencePosition(position)
	return w
}

func TestReferenceSerialIdentityDiagnostics(t *testing.T) {
	serialIdentityScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		serialIdentityRun(t, p, b, c, referenceExec(`CREATE TABLE owned(id SERIAL)`, 0), referenceExec(`CREATE TABLE "Mixed"("ID" SERIAL)`, 0))
		for _, tc := range []struct{ call, signature string }{
			{`pg_get_serial_sequence()`, `pg_get_serial_sequence()`},
			{`pg_get_serial_sequence('owned')`, `pg_get_serial_sequence(unknown)`},
			{`pg_get_serial_sequence('owned','id',1)`, `pg_get_serial_sequence(unknown, unknown, integer)`},
			{`pg_get_serial_sequence(1,'id')`, `pg_get_serial_sequence(integer, unknown)`},
			{`pg_get_serial_sequence('owned',1)`, `pg_get_serial_sequence(unknown, integer)`},
			{`pg_get_serial_sequence(true,'id')`, `pg_get_serial_sequence(boolean, unknown)`},
			{`pg_get_serial_sequence('owned',true)`, `pg_get_serial_sequence(unknown, boolean)`},
			{`pg_get_serial_sequence(NULL::integer,NULL)`, `pg_get_serial_sequence(integer, unknown)`},
			{`pg_get_serial_sequence(NULL,NULL::integer)`, `pg_get_serial_sequence(unknown, integer)`},
			{`pg_get_serial_sequence(1::integer,'id'::text)`, `pg_get_serial_sequence(integer, text)`},
			{`public.pg_get_serial_sequence('owned','id')`, `public.pg_get_serial_sequence(unknown, unknown)`},
			{`"PG_GET_SERIAL_SEQUENCE"('owned','id')`, `PG_GET_SERIAL_SEQUENCE(unknown, unknown)`},
			{`pg_get_serial_sequenc('owned','id')`, `pg_get_serial_sequenc(unknown, unknown)`},
			{`pg_get_owned_sequence('owned','id')`, `pg_get_owned_sequence(unknown, unknown)`},
		} {
			for _, prepared := range []bool{false, true} {
				name := tc.call + "/direct"
				if prepared { name = tc.call + "/prepare" }
				t.Run(name, func(t *testing.T) {
					s := referenceErrorDiagnostic("SELECT "+tc.call, serialIdentityMissingFunction(tc.signature, 8))
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared { s.errorStage = "prepare" }
					serialIdentityRun(t, p, b, c, s)
				})
			}
		}
		q := `SELECT 'été', pg_get_serial_sequence()`
		w := serialIdentityMissingFunction("pg_get_serial_sequence()", utf8.RuneCountInString(q[:strings.Index(q, "pg_get")])+1)
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("unicode-position/prepared=%t", prepared), func(t *testing.T) {
				s := referenceErrorDiagnostic(q, w)
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				if prepared { s.errorStage = "prepare" }
				serialIdentityRun(t, p, b, c, s)
			})
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("missing-function-schema/prepared=%t", prepared), func(t *testing.T) {
				w := controlsReferenceDiagnostic("3F000", `schema "si_absent_schema" does not exist`)
				w.Position = "8"
				s := referenceErrorDiagnostic(`SELECT si_absent_schema.pg_get_serial_sequence('owned','id')`, w)
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				if prepared { s.errorStage = "prepare" }
				serialIdentityRun(t, p, b, c, s)
			})
		}
		held := serialIdentityHeld(t, p, b, c)
		// Pinned PostgreSQL18.0 records include empty Context/Position and every
		// other unspecified portable field, not only SQLSTATE/message.
		cases := []struct{ table, column, code, message string }{
			{"si_missing", "id", "42P01", `relation "si_missing" does not exist`},
			{"public.si_missing", "id", "42P01", `relation "public.si_missing" does not exist`},
			{"si_absent_schema.owned", "id", "3F000", `schema "si_absent_schema" does not exist`},
			{"owned", "Col", "42703", `column "Col" of relation "owned" does not exist`},
			{"public.owned", "Col", "42703", `column "Col" of relation "owned" does not exist`},
			{`public."Mixed"`, "id", "42703", `column "id" of relation "Mixed" does not exist`},
			{`public."Mixed"`, `"ID"`, "42703", `column ""ID"" of relation "Mixed" does not exist`},
			{`public."Mixed"`, " ID ", "42703", `column " ID " of relation "Mixed" does not exist`},
			{"", "id", "42602", `invalid name syntax`},
			{" \t\n", "id", "42602", `invalid name syntax`},
			{"owned.", "id", "42602", `invalid name syntax`},
			{`"owned`, "id", "42602", `invalid name syntax`},
			{`""`, "id", "42P01", `relation "" does not exist`},
			{`public.""`, "id", "42P01", `relation "public." does not exist`},
			{`"".owned`, "id", "3F000", `schema "" does not exist`},
			{"a.b.c.d", "id", "42601", `improper relation name (too many dotted names): a.b.c.d`},
			{"si_other_database.public.owned", "id", "0A000", `cross-database references are not implemented: "si_other_database.public.owned"`},
		}
		for _, tc := range cases {
			t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
				w := controlsReferenceDiagnostic(tc.code, tc.message)
				t.Run("direct", func(t *testing.T) {
					s := referenceErrorDiagnostic(`SELECT pg_get_serial_sequence(`+pq.QuoteLiteral(tc.table)+`,`+pq.QuoteLiteral(tc.column)+`)`, w)
					s.selectRows, s.errorStage = true, "execute"
					serialIdentityRun(t, p, b, c, s)
				})
				t.Run("held-execute", func(t *testing.T) {
					s := referenceErrorDiagnostic(serialIdentityQuery, w, tc.table, tc.column)
					s.selectRows, s.errorStage = true, "execute"
					serialIdentityRun(t, p, b, held, s)
				})
				t.Run("held-recovery", func(t *testing.T) {
					serialIdentityCheck(t, p, b, c, held, "owned", "id", "public.owned_id_seq")
				})
			})
		}
	})
}
