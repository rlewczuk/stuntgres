package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSimilarReviewDefault(t *testing.T) {
	p := newReferenceScenario(t)
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
	for _, b := range p.backends() {
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				create := referenceExec(`CREATE TABLE extraction_default(piece TEXT DEFAULT `+c.expression+`)`, 0)
				create.prepared = prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, create)
				inserted := referenceExec(`INSERT INTO extraction_default DEFAULT VALUES`, 1)
				inserted.prepared = prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, inserted)
				selected := referenceSelect(`SELECT piece FROM extraction_default`, []string{"piece"}, []string{"TEXT"}, [][]any{{c.piece}})
				selected.checkMetadata = similarReferenceDeclaredMetadata(b.pg)
				similarReferenceRoutes(t, p, b, selected)
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`DROP TABLE extraction_default`, 0))
			}
		}
	}
}

func TestReferenceSimilarReviewMalformedDefault(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, prepared := range []bool{false, true} {
			create := referenceExec(`CREATE TABLE bad_extraction_default(piece TEXT DEFAULT substring('a' SIMILAR '[' ESCAPE '#'))`, 0)
			create.prepared = prepared
			referenceRunStep(t, p.ctx, b.db, b.recorder, create)
			// PostgreSQL18.0 observes symbolic CREATE followed by this demanded INSERT
			// failure in both execution routes, with the SQL wrapper's exact Context.
			failure := referenceImplicitError(`INSERT INTO bad_extraction_default DEFAULT VALUES`, referenceDiagnostic{Code: "2201B", Message: "invalid regular expression: brackets [] not balanced", Context: `SQL function "substring" statement 1`})
			failure.errorStage = "execute"
			for _, insertPrepared := range []bool{false, true} {
				failure.prepared = insertPrepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, failure)
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect(`SELECT piece FROM bad_extraction_default`, []string{"piece"}, []string{"TEXT"}, nil))
			for _, insertPrepared := range []bool{false, true} {
				override := referenceExec(`INSERT INTO bad_extraction_default(piece) VALUES('safe')`, 1)
				override.prepared = insertPrepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, override)
			}
			similarReferenceRoutes(t, p, b, referenceSelect(`SELECT piece FROM bad_extraction_default`, []string{"piece"}, []string{"TEXT"}, [][]any{{"safe"}, {"safe"}}))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`DROP TABLE bad_extraction_default`, 0))
		}
	}
}

func TestReferenceSimilarReviewDefaultControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		if b.pg {
			continue
		} // Native host controls; emitted SQL is replayed on PostgreSQL below.
		for _, s := range []referenceStep{
			referenceExec(`CREATE TABLE defaults_replay(a TEXT DEFAULT substring('abc' SIMILAR '%' ESCAPE '#'),b TEXT DEFAULT substring('ab','a#"#"b','#'),c TEXT DEFAULT pg_catalog.substring(NULL::text,'%','#'))`, 0),
			referenceExec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1),
		} {
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		}
		want := referenceSelect(`SELECT a,b,c FROM defaults_replay`, []string{"a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "", nil}})
		want.checkMetadata = similarReferenceDeclaredMetadata(false)
		referenceRunStep(t, p.ctx, b.db, b.recorder, want)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("extraction DEFAULT replay changed symbolic dump")
		}
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1))
		want.rows = [][]any{{"abc", "", nil}, {"abc", "", nil}}
		similarReferenceRoutes(t, p, b, want)
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec(`INSERT INTO defaults_replay DEFAULT VALUES`, 1))
			want.checkMetadata = similarReferenceDeclaredMetadata(true)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
		}
	}
}

func TestReferenceSimilarReviewDefaultBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	// These are retained native capability boundaries, not PostgreSQL exclusions.
	for _, b := range p.backends() {
		if b.pg {
			continue
		}
		for _, expression := range []string{
			`substring('abc',1,2)`,
			`substring('abc','a')`,
			`substring('abc'::bytea,1,2)`,
			`length('abc'::bytea)`,
		} {
			for _, prepared := range []bool{false, true} {
				q := `CREATE TABLE default_boundary(v TEXT DEFAULT ` + expression + `)`
				function := expression[:strings.Index(expression, "(")]
				s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, function) + 1)})
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		}
	}
}
