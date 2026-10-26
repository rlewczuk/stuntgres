package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Released uncertainty arithmetic can produce canonical output which seg_in
// itself does not accept. Both backends exercise the original source spellings;
// the native-only control phase preserves these datums without text reinput.
func TestReferenceSegDerivedEdges(t *testing.T) {
	for _, tc := range []struct{ input, canonical string }{
		{"0(+-)-1", "1 .. -1"},
		{"3.4e38(+-)3.4e38", "0.0 .. Infinity"},
		{"-3.4e38(+-)3.4e38", "-Infinity .. 0.0"},
		{"3.4e38(+-)-3.4e38", "Infinity .. 0.0"},
		{"-3.4e38(+-)-3.4e38", "0.0 .. -Infinity"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE seg_derived(id integer NOT NULL,v seg)", 0))
				run(fixture.Exec("INSERT INTO seg_derived VALUES (1,"+queryIntLiteral(tc.input)+")", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO seg_derived VALUES (2,$1)", 1, tc.input)))
				check := segSelect(b, "SELECT id,v FROM seg_derived ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), tc.canonical}, {int64(2), tc.canonical}})
				check.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, e := rows.ColumnTypes()
					if e != nil {
						t.Fatal(e)
					}
					segMetadata(t, b.PG, cols, check.Columns, []bool{false, true})
				}
				run(check)
				run(fixture.Prepared(check))
				segCatalog(t, p, b, c)
				want := fixture.ControlsReferenceDiagnostic("42601", "bad seg representation")
				want.Detail = `syntax error at or near "I"`
				if strings.Contains(tc.canonical, "-Infinity") {
					want.Detail = `syntax error at or near "-"`
				}
				if tc.input == "0(+-)-1" {
					want = fixture.ControlsReferenceDiagnostic("22023", "swapped boundaries: 1 is greater than -1")
				}
				want.Position = "8"
				for _, prepared := range []bool{false, true} {
					queryIntError(t, p, b, c, "SELECT "+queryIntLiteral(tc.canonical)+"::seg", prepared, true, true, want)
				}
				for _, prepared := range []bool{false, true} {
					literal := want
					literal.Position = "43"
					queryIntError(t, p, b, c, "INSERT INTO seg_derived VALUES (3,'1'),(4,"+queryIntLiteral(tc.canonical)+")", prepared, false, true, literal)
					run(check)
					bound := queryIntParameterDiagnostic(want.Code, want.Message, tc.canonical, 2)
					bound.Detail = want.Detail
					queryIntError(t, p, b, c, "INSERT INTO seg_derived VALUES (3,$1),(4,$2)", prepared, false, false, bound, "1", tc.canonical)
					run(check)
				}
				run(check)
				if !b.PG {
					var dump bytes.Buffer
					if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
						t.Fatal(e)
					}
					old, e := b.DB.QueryContext(p.Ctx, check.Query)
					if e != nil {
						t.Fatal(e)
					}
					defer old.Close()
					cols, e := old.ColumnTypes()
					if e != nil {
						t.Fatal(e)
					}
					if e = stuntgres.Reset(p.Ctx, p.Native); e != nil {
						t.Fatal(e)
					}
					if e = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
						t.Fatal(e)
					}
					run(check)
					queryIntConsumeRetained(t, old, check)
					segMetadata(t, false, cols, check.Columns, []bool{false, true})
					segCatalog(t, p, b, c)
					var again bytes.Buffer
					if e = stuntgres.Dump(p.Ctx, p.Native, &again); e != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
						t.Fatal("derived exact redump", e)
					}
					target := fixture.NewFixture(t, false, true)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE seg_edge_spacer(v integer)", 0))
					if e = stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); e != nil {
						t.Fatal(e)
					}
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
					segCatalog(t, p, fixture.Backend{Recorder: target.MemoryRecord}, target.Memory)
					bad := strings.Replace(dump.String(), "stuntgres-domain-null-v1", "stuntgres-domain-null-v999", 1)
					if bad == dump.String() {
						t.Fatal("missing exceptional replay payload")
					}
					if e = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)); e == nil {
						t.Fatal("corrupt exceptional replay accepted")
					}
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, check)
					again.Reset()
					if e = stuntgres.Dump(target.Ctx, target.Native, &again); e != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
						t.Fatal("failed exceptional replay changed target", e)
					}
				}
				for _, q := range []string{"DROP TABLE seg_derived", "DROP EXTENSION seg", "CREATE EXTENSION seg", "CREATE TABLE seg_derived(id integer NOT NULL,v seg)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO seg_derived VALUES (1,"+queryIntLiteral(tc.input)+")", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO seg_derived VALUES (2,$1)", 1, tc.input)))
				run(check)
				run(fixture.Prepared(check))
				segCatalog(t, p, b, c)
			})
		})
	}
}
