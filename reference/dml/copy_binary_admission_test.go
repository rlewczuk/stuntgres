package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCopyBinaryAdmission(t *testing.T) {
	cases := []struct {
		name, query string
		setup       []string
		to          bool
	}{
		{name: "missing_relation_before_option", query: "COPY missing FROM STDIN WITH(FORMAT binary,HEADER true)"},
		{name: "missing_column_before_option", query: "COPY copy_binary_admit(missing) FROM STDIN WITH(FORMAT binary,HEADER true)"},
		{name: "duplicate_column_before_option", query: "COPY copy_binary_admit(i,i) FROM STDIN WITH(FORMAT binary,HEADER true)"},
		{name: "generated_column", query: "COPY copy_binary_admit(g) FROM STDIN WITH(FORMAT binary)"},
		{name: "query_option_before_analysis", query: "COPY (SELECT missing FROM missing) TO STDOUT WITH(FORMAT binary,HEADER true)", to: true},
		{name: "view_from_kind", query: "COPY copy_binary_view FROM STDIN WITH(FORMAT binary)", setup: []string{"CREATE VIEW copy_binary_view AS SELECT i FROM copy_binary_admit"}},
		{name: "view_to_kind", query: "COPY copy_binary_view TO STDOUT WITH(FORMAT binary)", setup: []string{"CREATE VIEW copy_binary_view AS SELECT i FROM copy_binary_admit"}, to: true},
		{name: "unquoted_mixed_case", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT BiNaRy)"},
		{name: "quoted_upper_case", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT 'BINARY')"},
		{name: "quoted_lower_case", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT 'binary')"},
		{name: "syntax_missing_close", query: "COPY copy_binary_admit(i FROM STDIN WITH(FORMAT binary)"},
		{name: "query_from", query: "COPY (SELECT 1) FROM STDIN WITH(FORMAT binary)"},
		{name: "denied_from_before_option", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT binary,HEADER true)", setup: []string{"CREATE ROLE binary_actor", "GRANT USAGE ON SCHEMA public TO binary_actor", "SET ROLE binary_actor"}},
		{name: "denied_to_before_option", query: "COPY copy_binary_admit TO STDOUT WITH(FORMAT binary,HEADER true)", setup: []string{"CREATE ROLE binary_actor", "GRANT USAGE ON SCHEMA public TO binary_actor", "SET ROLE binary_actor"}, to: true},
		{name: "column_acl_from_allowed", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT binary)", setup: []string{"CREATE ROLE binary_actor", "GRANT USAGE ON SCHEMA public TO binary_actor", "GRANT INSERT(i),SELECT(i) ON copy_binary_admit TO binary_actor", "SET ROLE binary_actor"}},
		{name: "column_acl_to_allowed", query: "COPY copy_binary_admit(i) TO STDOUT WITH(FORMAT binary)", setup: []string{"CREATE ROLE binary_actor", "GRANT USAGE ON SCHEMA public TO binary_actor", "GRANT INSERT(i),SELECT(i) ON copy_binary_admit TO binary_actor", "SET ROLE binary_actor"}, to: true},
		{name: "column_acl_from_denied", query: "COPY copy_binary_admit(v) FROM STDIN WITH(FORMAT binary)", setup: []string{"CREATE ROLE binary_actor", "GRANT USAGE ON SCHEMA public TO binary_actor", "GRANT INSERT(i),SELECT(i) ON copy_binary_admit TO binary_actor", "SET ROLE binary_actor"}},
		{name: "read_only", query: "COPY copy_binary_admit(i) FROM STDIN WITH(FORMAT binary)", setup: []string{"BEGIN READ ONLY"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, q := range tc.setup {
				if q == "CREATE ROLE binary_actor" {
					fixture.RoleReferenceCleanup(t, []string{"binary_actor"})
				}
			}
			copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
				c.exec(t, "CREATE TABLE copy_binary_admit(i int4,v text DEFAULT 'fallback',g int4 GENERATED ALWAYS AS(i+1) STORED)")
				c.exec(t, "INSERT INTO copy_binary_admit(i,v) VALUES(99,'sentinel')")
				for _, q := range tc.setup {
					// Existing native PUBLIC schema usage is already granted; no adjacent
					// schema privilege implementation is introduced by this COPY witness.
					if !c.pg && strings.HasPrefix(q, "GRANT USAGE ON SCHEMA") {
						continue
					}
					c.exec(t, q)
				}
				reader := &copyTextChunks{text: string(copyBinaryStream(copyBinaryRow(copyBinary32(42)))), widths: []int{1}}
				var result copyTextResult
				var err error
				var output bytes.Buffer
				if tc.to {
					result, err = c.to(tc.query, &output)
				} else {
					result, err = c.from(tc.query, reader)
				}
				copyBinaryAdditionalOutcome(t, result, err, output.Bytes())
				if !c.pg && err != nil && reader.offset != 0 {
					t.Errorf("denied native SQL read %d bytes", reader.offset)
				}
				// End failed transactions/roles before inspecting committed authority.
				c.exec(t, "ROLLBACK")
				c.exec(t, "RESET ROLE")
				if err != nil {
					c.selectRows(t, "SELECT * FROM copy_binary_admit", []string{"i", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(99), "sentinel", int64(100)}})
				}
				if !tc.to && err == nil {
					copyTextSuccess(t, result, err, 1)
					c.selectRows(t, "SELECT * FROM copy_binary_admit ORDER BY i", []string{"i", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(42), "fallback", int64(43)}, {int64(99), "sentinel", int64(100)}})
				}
			})
		})
	}
}

func TestReferenceCopyBinaryDatabaseSQLBoundary(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_boundary(v int4)")
		if b.PG {
			result, err := c.from("COPY copy_binary_boundary FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(7)))))
			copyTextSuccess(t, result, err, 1)
			c.output(t, "COPY copy_binary_boundary TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(copyBinary32(7)))), 1)
			reader := &copyTextNoRead{}
			result, err = stuntgres.CopyFrom(c.ctx, c.conn, "COPY copy_binary_boundary FROM STDIN WITH(FORMAT binary)", reader)
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) || result != (copyTextResult{}) || reader.calls != 0 {
				t.Fatal("foreign transport boundary", result, err, reader.calls)
			}
			return
		}
		for _, tc := range []struct{ query, message string }{
			{"COPY missing FROM STDIN WITH(FORMAT binary)", "COPY FROM STDIN requires stuntgres.CopyFrom"},
			{"COPY missing TO STDOUT WITH(FORMAT binary)", "COPY TO STDOUT requires stuntgres.CopyTo"},
			{"COPY missing FROM '/never-open-copy-binary' WITH(FORMAT binary)", "COPY server files are unsupported"},
			{"COPY missing TO '/never-open-copy-binary' WITH(FORMAT binary)", "COPY server files are unsupported"},
			{"COPY missing FROM PROGRAM 'never-execute-copy-binary' WITH(FORMAT binary)", "COPY PROGRAM is unsupported"},
			{"COPY missing TO PROGRAM 'never-execute-copy-binary' WITH(FORMAT binary)", "COPY PROGRAM is unsupported"},
		} {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message}
			for _, db := range []interface {
				ExecContext(context.Context, string, ...any) (sql.Result, error)
				QueryContext(context.Context, string, ...any) (*sql.Rows, error)
				PrepareContext(context.Context, string) (*sql.Stmt, error)
			}{b.DB, c.conn} {
				result, err := db.ExecContext(c.ctx, tc.query)
				if result != nil {
					t.Fatal("ordinary COPY exposed result")
				}
				fixture.AssertDiagnostic(t, err, &want)
				rows, err := db.QueryContext(c.ctx, tc.query)
				if rows != nil {
					rows.Close()
					t.Fatal("ordinary COPY exposed Rows")
				}
				fixture.AssertDiagnostic(t, err, &want)
				stmt, err := db.PrepareContext(c.ctx, tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("ordinary COPY exposed prepared stream")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
			if strings.Contains(tc.query, "PROGRAM") || strings.Contains(tc.query, "never-open") {
				reader := &copyTextNoRead{}
				var result copyTextResult
				var err error
				if strings.Contains(tc.query, " FROM ") {
					result, err = c.from(tc.query, reader)
				} else {
					result, err = c.to(tc.query, io.Discard)
				}
				copyTextDiagnostic(t, result, err, want)
				if reader.calls != 0 {
					t.Fatal("resource boundary called reader")
				}
			}
		}
		wrapped, err := p.Memory.Conn(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer wrapped.Close()
		result, err := stuntgres.CopyFrom(c.ctx, wrapped, "COPY copy_binary_boundary FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream()))
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || result != (copyTextResult{}) {
			t.Fatal("wrapped transport boundary", result, err)
		}
	})
}
