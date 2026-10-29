package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"comment_ext_reader"})
	extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'old'", 0))
		oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
		run(fixture.Exec("CREATE TABLE missing_comment_ext(v INT)", 0))
		run(fixture.Exec("INSERT INTO missing_comment_ext VALUES(77)", 1))
		for _, prepared := range []bool{false, true} {
			for _, query := range []bool{false, true} {
				for _, name := range []string{"missing_comment_ext", "absent_comment_ext"} {
					s := fixture.RoleReferenceError("COMMENT ON EXTENSION "+name+" IS 'bad'", "42704", `extension "`+name+`" does not exist`, "", prepared, "execute")
					s.SelectRows = query
					run(s)
				}
				for _, tc := range []struct{ sql, token string }{
					{"COMMENT ON EXTENSION public.fuzzystrmatch IS 'bad'", "."},
					{"COMMENT ON EXTENSION IF EXISTS fuzzystrmatch IS 'bad'", "EXISTS"},
					{"COMMENT ON EXTENSION fuzzystrmatch,plpgsql IS 'bad'", ","},
					{"COMMENT ON EXTENSION fuzzystrmatch IS $1", "$1"},
					{"COMMENT ON EXTENSION fuzzystrmatch IS 42", "42"},
					{"COMMENT ON EXTENSION fuzzystrmatch IS 'bad' || 'suffix'", "||"},
					{"COMMENT ON EXTENSION fuzzystrmatch 'bad'", "'bad'"},
					{"COMMENT ON EXTENSION fuzzystrmatch IS", ""},
					{"COMMENT ON EXTENSION fuzzystrmatch IS '雪' trailing", "trailing"},
				} {
					pos := utf8.RuneCountInString(tc.sql) + 1
					message := "syntax error at end of input"
					if tc.token != "" {
						pos = utf8.RuneCountInString(tc.sql[:strings.Index(tc.sql, tc.token)]) + 1
						token := tc.token
						message = `syntax error at or near "` + token + `"`
					}
					s := fixture.ImplicitError(tc.sql, fixture.Diagnostic{Code: "42601", Message: message, Position: strconv.Itoa(pos)})
					s.Prepared, s.SelectRows = prepared, query
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					extensionCommentValue(t, p, b, c, oid, "old")
				}
			}
		}
		q := "COMMENT ON EXTENSION fuzzystrmatch IS 'not authorized'"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("CREATE USER comment_ext_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION comment_ext_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, held := range []bool{false, true} {
			var x fixture.Executor = c
			if held {
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of extension fuzzystrmatch"}))
			extensionCommentValue(t, p, b, c, oid, "old")
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT v FROM missing_comment_ext", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
	})
}
