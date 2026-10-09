package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func formatReferenceMetadata(t *testing.T, pg bool, rows *sql.Rows, storedView bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() {
			t.Fatalf("TEXT metadata: %s/%v", column.DatabaseTypeName(), column.ScanType())
		}
		length, lengthKnown := column.Length()
		wantLength := int64(9223372036854775807)
		if !lengthKnown || length != wantLength {
			t.Fatalf("TEXT Length %d/%t, want %d/true", length, lengthKnown, wantLength)
		}
		precision, scale, decimalKnown := column.DecimalSize()
		if precision != 0 || scale != 0 || decimalKnown {
			t.Fatalf("TEXT DecimalSize %d/%d/%t", precision, scale, decimalKnown)
		}
		nullable, nullableKnown := column.Nullable()
		// Native stored view columns have catalog-known nullable descriptors;
		// direct computed format and pq descriptors retain unknown nullability.
		wantNullable := storedView && !pg
		if nullable != wantNullable || nullableKnown != wantNullable {
			t.Fatalf("Nullable %t/%t, want %t/%t (stored view %t, pg %t)", nullable, nullableKnown, wantNullable, wantNullable, storedView, pg)
		}
	}
}

func TestReferenceFormatMetadata(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format_meta(p text,v text)", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO format_meta VALUES(NULL,'unused'),('%s',''),('%s','x')", 3))
			}
			for _, q := range []string{"SELECT format(p,v) FROM format_meta ORDER BY v", "SELECT format(p,v) AS rendered FROM format_meta ORDER BY v"} {
				t.Run(q+"/"+map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
					name := "format"
					if strings.Contains(q, " AS ") {
						name = "rendered"
					}
					want := [][]any{}
					if populated {
						want = [][]any{{""}, {nil}, {"x"}}
					}
					s := referenceSelect(q, []string{name}, []string{"TEXT"}, want)
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) { formatReferenceMetadata(t, b.pg, rows, false) }
					formatReferenceBoth(t, p, b, c, s)
				})
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW format_meta_view AS SELECT format(p,v) AS rendered FROM format_meta", 0))
		s := referenceSelect("SELECT rendered FROM format_meta_view ORDER BY rendered", []string{"rendered"}, []string{"TEXT"}, [][]any{{""}, {"x"}, {nil}})
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) { formatReferenceMetadata(t, b.pg, rows, true) }
		formatReferenceBoth(t, p, b, c, s)
	})
}

func TestReferenceFormatCollations(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, tc := range []struct{ q, want string }{
			{`SELECT lower(format('%s','É' COLLATE "C")) AS v`, "É"},
			{`SELECT lower(format('%s','É' COLLATE pg_unicode_fast)) AS v`, "é"},
			{`SELECT lower(format('%s' COLLATE pg_unicode_fast,'É')) AS v`, "é"},
			{`SELECT lower(format('É','unused' COLLATE pg_unicode_fast)) AS v`, "é"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				formatReferenceBoth(t, p, b, c, referenceSelect(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
			})
		}
		for _, q := range []string{
			`SELECT format('%s' COLLATE "C",'x' COLLATE "POSIX")`,
			`SELECT format(NULL::text COLLATE "C",'unused' COLLATE "POSIX")`,
			`SELECT format('literal','x' COLLATE "C",'unused' COLLATE "POSIX")`,
		} {
			t.Run(q, func(t *testing.T) {
				d := controlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
				d.Position = referencePosition(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
				formatReferenceFailure(t, p, b, c, q, d, true)
			})
		}
		run(referenceExec(`CREATE TABLE format_coll(a text COLLATE "C",b text COLLATE "POSIX")`, 0))
		run(referenceExec("INSERT INTO format_coll VALUES('É','x'),(NULL,'y')", 2))
		for _, q := range []string{"SELECT format('%s/%s',a,b) AS v FROM format_coll WHERE a IS NOT NULL", "SELECT format('É',a,b) AS v FROM format_coll WHERE a IS NOT NULL"} {
			want := "É/x"
			if strings.Contains(q, "format('É'") {
				want = "É"
			}
			formatReferenceBoth(t, p, b, c, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
		}
		d := controlsReferenceDiagnostic("42P22", "could not determine which collation to use for lower() function")
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		formatReferenceFailure(t, p, b, c, "SELECT lower(format('%s/%s',a,b)) FROM format_coll WHERE a IS NOT NULL", d, false)
		formatReferenceBoth(t, p, b, c, referenceSelect(`SELECT lower(format('%s/%s',a,b) COLLATE "C") AS v FROM format_coll WHERE a IS NOT NULL`, []string{"v"}, []string{"TEXT"}, [][]any{{"É/x"}}))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT lower(format(NULL,a,b)) AS v FROM format_coll", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}}))
		d = controlsReferenceDiagnostic("42P22", `could not determine which collation to use for view column "v"`)
		d.Hint = "Use the COLLATE clause to set the collation explicitly."
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic("CREATE VIEW format_conflict AS SELECT format('%s/%s',a,b) AS v FROM format_coll", d)
			s.errorStage = "execute"
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		run(referenceExec(`CREATE VIEW format_conflict AS SELECT format('%s/%s',a,b) COLLATE "C" AS v FROM format_coll`, 0))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT lower(v) AS v FROM format_conflict ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"/y"}, {"É/x"}}))
		run(referenceExec(`CREATE VIEW format_collated AS SELECT format('%s',a COLLATE pg_unicode_fast) AS v FROM format_coll`, 0))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT lower(v) AS v FROM format_collated ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{""}, {"é"}}))
	})
}
