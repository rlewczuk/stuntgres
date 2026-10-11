package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesSourceValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			peer := db
			if !backend.PG {
				peer = p.Native
			}
			run := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, db, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,label TEXT)", 0))
			want := [][]any{}
			for _, tc := range unicodeEscapeValues() {
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
					s.Prepared = prepared
					run(s)
					want = append(want, []any{int64(len(want) + 1), tc.text})
				}
				ids := [][]any{}
				for _, row := range want {
					if row[1] == tc.text {
						ids = append(ids, []any{row[0]})
					}
				}
				s := fixture.Select("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, peer, backend.Recorder, s)
				run(fixture.Prepared(s))
			}
			s := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want)
			s.NonContext = true
			fixture.RunStep(t, p.Ctx, peer, backend.Recorder, s)
			metadata := s
			metadata.Query, metadata.Bag = "SELECT * FROM t", true
			rows, err := db.Query(metadata.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, metadata, nil)
			run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
			run(fixture.Select("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
			run(fixture.Exec(`SET application_name TO U&'\0041'`, 0))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
		})
	}
}

func TestReferenceUnicodeEscapesSourceNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		emoji := `U&'\D83D\DE00'`
		t.Run(fmt.Sprint(prepared)+"/"+emoji, func(t *testing.T) {
			p := fixture.NewScenario(t)
			step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
			for _, tc := range unicodeEscapeNames() {
				quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
					fixture.Exec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
					fixture.Exec("INSERT INTO "+quoted+" VALUES (2,"+emoji+")", 1),
					fixture.Select("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					fixture.Select("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
				} {
					s.Prepared = prepared && !s.SelectRows
					step(s)
				}
				for _, backend := range p.Backends() {
					q := "SELECT * FROM " + tc.source + " ORDER BY id"
					stmt, err := backend.DB.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := stmt.Query()
					if err != nil {
						stmt.Close()
						t.Fatal(err)
					}
					fixture.LexicalRows(t, backend.PG, rows, fixture.Select(q, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}), nil)
					stmt.Close()
				}
			}
			native := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`, Position: "15"}
			s := fixture.Step{Query: "SELECT * FROM a", SelectRows: true, State: "42P01", Diagnostic: &native, ErrorStage: "execute"}
			step(s)
			step(fixture.ErrorDiagnostic(`CREATE TABLE U&"`+strings.Repeat(`\0062`, 65)+`"(x TEXT)`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "` + strings.Repeat("b", 63) + `" already exists`}))
			step(fixture.Select(`SELECT * FROM U&"\0070ublic".U&"\0041" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}))
		})
	}
}
