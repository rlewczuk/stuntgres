package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

var ftsDebugColumns = []string{"alias", "description", "token", "dictionaries", "dictionary", "lexemes"}
var ftsDebugTypes = []string{"TEXT", "TEXT", "TEXT", "_REGDICTIONARY", "REGDICTIONARY", "_TEXT"}
var ftsStatColumns = []string{"word", "ndoc", "nentry"}
var ftsStatTypes = []string{"TEXT", "INT4", "INT4"}

func ftsDebugEnglish() [][]any {
	return [][]any{{"asciiword", "Word, all ASCII", "The", "{english_stem}", "english_stem", "{}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "Rats", "{english_stem}", "english_stem", "{rat}"}}
}
func ftsDebugSimple() [][]any {
	return [][]any{{"asciiword", "Word, all ASCII", "The", "{simple}", "simple", "{the}"}, {"blank", "Space symbols", " ", "{}", nil, nil}, {"asciiword", "Word, all ASCII", "Rats", "{simple}", "simple", "{rats}"}}
}
func ftsStatBase() [][]any {
	return [][]any{{"cat", int64(2), int64(3)}, {"dog", int64(2), int64(3)}, {"solo", int64(1), int64(1)}}
}
func ftsInspectionConnection(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func ftsInspectionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
func ftsStatSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE fts_stat_documents(id integer,vector tsvector)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO fts_stat_documents VALUES(1,'cat:1A,2B dog:3D solo'),(2,'cat:4C dog:1A,2D'),(3,''),(4,NULL)", 4))
}
func ftsInspectionDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic, eager bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want, args...)
			s.Prepared = prepared
			s.SelectRows = queryMode
			s.ErrorStage = "execute"
			if prepared && eager {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
	if eager {
		return
	}
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	for range 2 {
		for _, queryMode := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want, args...)
			s.SelectRows = queryMode
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	}
}
