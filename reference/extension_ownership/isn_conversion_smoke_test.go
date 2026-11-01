package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsnConversionFeatureSmoke(t *testing.T) {
	isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		for _, q := range []string{
			"CREATE EXTENSION isn",
			"CREATE TABLE isn_smoke(id integer NOT NULL,v ean13)",
			"CREATE VIEW isn_smoke_view AS SELECT id,v='123456789X'::isbn AS equal,v::isbn::text AS book FROM isn_smoke WHERE id<>3",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO isn_smoke VALUES(1,'9781234567897'),(2,'9781234567897!'),(3,'0123456789012'),(4,NULL),(5,'9781234567897')", 5))
		isnConversionSmokeCheck(t, p, b, c)
		held := fixture.IdentityHeld(t, p.Ctx, c, "SELECT ($1::isbn = $2::isbn13) AS equal")
		defer held.Statement.Close()
		textHeld := fixture.IdentityHeld(t, p.Ctx, c, "SELECT ($1::text::isbn = $2::text::isbn13) AS equal")
		defer textHeld.Statement.Close()
		for _, tc := range []struct{ left, right, want any }{
			{"123456789X", "9781234567897", true},
			{[]byte("1-234-56789-X"), []byte("9781234567897"), true},
			{nil, "9781234567897", nil},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"equal"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right))
			fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, fixture.Select(textHeld.SQL, []string{"equal"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right))
		}
		bad := "INSERT INTO isn_smoke VALUES(6,'9781234567897'::ean13::isbn::ean13),(7,'9790123456785'::ean13::isbn::ean13)"
		want := isnConversionDiagnostic("22P02", `cannot cast EAN13(ISMN) to ISBN for number: "9790123456785"`)
		run(isnConversionRuntimeError(bad, want))
		run(fixture.Prepared(isnConversionRuntimeError(bad, want)))
		run(fixture.OwnershipCount("SELECT count(*) FROM isn_smoke", 5))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"equal"}, []string{"BOOL"}, [][]any{{true}}, "123456789X", "9781234567897"))
		isnConversionSmokeCheck(t, p, b, c)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		old, err := c.QueryContext(p.Ctx, "SELECT id FROM isn_smoke ORDER BY v ASC NULLS LAST,id ASC")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		columns, err := old.ColumnTypes()
		if err != nil || len(columns) != 1 || columns[0].Name() != "id" || columns[0].DatabaseTypeName() != "INT4" {
			t.Fatal("original smoke metadata", columns, err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		isnConversionSmokeCheck(t, p, b, c)
		// Native replacement remints installed parameter identities; the held
		// signature cannot adopt a new ISBN role merely because its name matches.
		fenced := fixture.ErrorDiagnostic(held.SQL, isnConversionDiagnostic("0A000", "cached plan must not change parameter type"), "123456789X", "9781234567897")
		fenced.SelectRows, fenced.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fenced)
		run(fixture.Prepared(fixture.Select(held.SQL, []string{"equal"}, []string{"BOOL"}, [][]any{{true}}, "123456789X", "9781234567897")))
		// Builtin TEXT parameters and BOOL results keep their fixed signature.
		// Their interior ISN casts must bind the genuine restored declarations.
		for _, tc := range []struct{ left, right, want any }{
			{"123456789X", "9781234567897", true},
			{[]byte("1-234-56789-X"), []byte("9781234567897"), true},
			{nil, "9781234567897", nil},
		} {
			fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, fixture.Select(textHeld.SQL, []string{"equal"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right))
		}
		fixture.RangeConsumeHeld(t, old, isnConversionIDs("SELECT id FROM isn_smoke ORDER BY v ASC NULLS LAST,id ASC", 3, 1, 5, 2, 4))
		isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, p.Native))
		isnConversionReplay(t, p, saved, isnConversionSmokeCheck)
	})
}

func isnConversionSmokeCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	isnConversionRoutes(t, p, b, c, isnConversionComparison("'123456789X'::isbn", "'9781234567897'::isbn13", 0, false))
	isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, "SELECT isbn('9780123456786'::ean13) AS called,'9780123456786'::ean13::isbn AS cast_value", []string{"called", "cast_value"}, []string{"ISBN", "ISBN"}, [][]any{{"0-12-345678-9", "0-12-345678-9"}}, nil))
	isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_smoke ORDER BY v ASC NULLS LAST,id ASC", 3, 1, 5, 2, 4))
	isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_smoke ORDER BY v DESC NULLS FIRST,id ASC", 4, 2, 1, 5, 3))
	isnConversionRoutes(t, p, b, c, fixture.Select("SELECT id,equal,book FROM isn_smoke_view ORDER BY id", []string{"id", "equal", "book"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), true, "1-234-56789-X"}, {int64(2), false, "1-234-56789-X!"}, {int64(4), nil, nil}, {int64(5), true, "1-234-56789-X"}}))
}
