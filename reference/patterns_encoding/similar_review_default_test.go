package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarReviewDefault(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := []struct {
		expression string
		piece      any
	}{
		{`substring('abc' SIMILAR '%' ESCAPE '#')`, "abc"},
		{`substring('abc' FROM '%' FOR '#')`, "abc"},
		{`substring('abc','%','#')`, "abc"},
		{`pg_catalog.substring('abc','%','#')`, "abc"},
		{`substring(NULL::text SIMILAR '%' ESCAPE '#')`, nil},
		{`substring('abc' SIMILAR NULL::text ESCAPE '#')`, nil},
		{`substring('abc' SIMILAR '%' ESCAPE NULL::text)`, nil},
		{`substring('ab' SIMILAR 'a#"#"b' ESCAPE '#')`, ""},
	}
	for _, b := range p.Backends() {
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				create := fixture.Exec(`CREATE TABLE extraction_default(piece TEXT DEFAULT `+c.expression+`)`, 0)
				create.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, create)
				inserted := fixture.Exec(`INSERT INTO extraction_default DEFAULT VALUES`, 1)
				inserted.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, inserted)
				selected := fixture.Select(`SELECT piece FROM extraction_default`, []string{"piece"}, []string{"TEXT"}, [][]any{{c.piece}})
				selected.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
				similarReferenceRoutes(t, p, b, selected)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`DROP TABLE extraction_default`, 0))
			}
		}
	}
}

func TestReferenceSimilarReviewMalformedDefault(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			create := fixture.Exec(`CREATE TABLE bad_extraction_default(piece TEXT DEFAULT substring('a' SIMILAR '[' ESCAPE '#'))`, 0)
			create.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, create)
			// PostgreSQL18.0 observes symbolic CREATE followed by this demanded INSERT
			// failure in both execution routes, with the SQL wrapper's exact Context.
			failure := fixture.ImplicitError(`INSERT INTO bad_extraction_default DEFAULT VALUES`, fixture.Diagnostic{Code: "2201B", Message: "invalid regular expression: brackets [] not balanced", Context: `SQL function "substring" statement 1`})
			failure.ErrorStage = "execute"
			for _, insertPrepared := range []bool{false, true} {
				failure.Prepared = insertPrepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, failure)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT piece FROM bad_extraction_default`, []string{"piece"}, []string{"TEXT"}, nil))
			for _, insertPrepared := range []bool{false, true} {
				override := fixture.Exec(`INSERT INTO bad_extraction_default(piece) VALUES('safe')`, 1)
				override.Prepared = insertPrepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, override)
			}
			similarReferenceRoutes(t, p, b, fixture.Select(`SELECT piece FROM bad_extraction_default`, []string{"piece"}, []string{"TEXT"}, [][]any{{"safe"}, {"safe"}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`DROP TABLE bad_extraction_default`, 0))
		}
	}
}

func TestReferenceSimilarReviewDefaultControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		if b.PG {
			continue
		} // Native host controls; emitted SQL is replayed on PostgreSQL below.
		for _, s := range []fixture.Step{
			fixture.Exec(`CREATE TABLE defaults_replay(a TEXT DEFAULT substring('abc' SIMILAR '%' ESCAPE '#'),b TEXT DEFAULT substring('ab','a#"#"b','#'),c TEXT DEFAULT pg_catalog.substring(NULL::text,'%','#'))`, 0),
			fixture.Exec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		want := fixture.Select(`SELECT a,b,c FROM defaults_replay`, []string{"a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "", nil}})
		want.CheckMetadata = similarReferenceDeclaredMetadata(false)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, want)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("extraction DEFAULT replay changed symbolic dump")
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1))
		want.Rows = [][]any{{"abc", "", nil}, {"abc", "", nil}}
		similarReferenceRoutes(t, p, b, want)
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1))
			want.CheckMetadata = similarReferenceDeclaredMetadata(true)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
		}
	}
}

func TestReferenceSimilarReviewDefaultBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// The authorized two-TEXT POSIX witness now crosses this boundary.
			for _, prepared := range []bool{false, true} {
				create := fixture.Exec(`CREATE TABLE default_posix_boundary(v TEXT DEFAULT substring('abc','a'))`, 0)
				create.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, create)
				insert := fixture.Exec(`INSERT INTO default_posix_boundary DEFAULT VALUES`, 1)
				insert.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, insert)
				want := fixture.Select(`SELECT v FROM default_posix_boundary`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}})
				want.Prepared = prepared
				want.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, want)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`DROP TABLE default_posix_boundary`, 0))
			}
			// The remaining exclusions are native boundaries, not PG exclusions.
			if b.PG {
				return
			}
			for _, expression := range []string{
				`substring('abc',1,2)`,
				`substring('abc'::bytea,1,2)`,
				`length('abc'::bytea)`,
			} {
				for _, prepared := range []bool{false, true} {
					q := `CREATE TABLE default_boundary(v TEXT DEFAULT ` + expression + `)`
					function := expression[:strings.Index(expression, "(")]
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, function) + 1)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
}
