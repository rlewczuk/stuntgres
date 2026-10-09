package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceUnicodeEscapesSourceValues(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			peer := db
			if !backend.pg {
				peer = p.native
			}
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,label TEXT)", 0))
			want := [][]any{}
			for _, tc := range unicodeEscapeValues() {
				for _, prepared := range []bool{false, true} {
					s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", len(want)+1, tc.source), 1)
					s.prepared = prepared
					run(s)
					want = append(want, []any{int64(len(want) + 1), tc.text})
				}
				ids := [][]any{}
				for _, row := range want {
					if row[1] == tc.text {
						ids = append(ids, []any{row[0]})
					}
				}
				s := referenceSelect("SELECT id FROM t WHERE label="+tc.source+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
				s.nonContext = true
				referenceRunStep(t, p.ctx, peer, backend.recorder, s)
				run(referencePrepared(s))
			}
			s := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, want)
			s.nonContext = true
			referenceRunStep(t, p.ctx, peer, backend.recorder, s)
			metadata := s
			metadata.query, metadata.bag = "SELECT * FROM t", true
			rows, err := db.Query(metadata.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, metadata, nil)
			run(referencePrepared(referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 100, `\0041`)))
			run(referenceSelect("SELECT label FROM t WHERE id=100", []string{"label"}, []string{"TEXT"}, [][]any{{`\0041`}}))
			run(referenceExec(`SET application_name TO U&'\0041'`, 0))
			run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"A"}}))
		})
	}
}

func TestReferenceUnicodeEscapesSourceNames(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		emoji := `U&'\D83D\DE00'`
		t.Run(fmt.Sprint(prepared)+"/"+emoji, func(t *testing.T) {
			p := newReferenceScenario(t)
			step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
			for _, tc := range unicodeEscapeNames() {
				quoted := `"` + strings.ReplaceAll(tc.name, `"`, `""`) + `"`
				for _, s := range []referenceStep{
					referenceExec("CREATE TABLE "+tc.source+`(id INT,U&"\+01F600" TEXT)`, 0),
					referenceExec("INSERT INTO "+tc.source+` VALUES (1,U&'\0041')`, 1),
					referenceExec("INSERT INTO "+quoted+" VALUES (2,"+emoji+")", 1),
					referenceSelect("SELECT * FROM "+tc.source+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
					referenceSelect("SELECT * FROM "+quoted+" ORDER BY id", []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}),
				} {
					s.prepared = prepared && !s.selectRows
					step(s)
				}
				for _, backend := range p.backends() {
					q := "SELECT * FROM " + tc.source + " ORDER BY id"
					stmt, err := backend.db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := stmt.Query()
					if err != nil {
						stmt.Close()
						t.Fatal(err)
					}
					lexicalRows(t, backend.pg, rows, referenceSelect(q, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}), nil)
					stmt.Close()
				}
			}
			native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "a" does not exist`, Position: "15"}
			s := referenceStep{query: "SELECT * FROM a", selectRows: true, state: "42P01", diagnostic: &native, errorStage: "execute"}
			step(s)
			step(referenceErrorDiagnostic(`CREATE TABLE U&"`+strings.Repeat(`\0062`, 65)+`"(x TEXT)`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "` + strings.Repeat("b", 63) + `" already exists`}))
			step(referenceSelect(`SELECT * FROM U&"\0070ublic".U&"\0041" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}}))
		})
	}
}
