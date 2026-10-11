package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceDiagnosticsConsumerRoutes(t *testing.T) {
	for _, tc := range []struct {
		setup, query string
		want         fixture.Diagnostic
	}{
		{"", "CREATE OR REPLACE VIEW v AS SELECT id FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot drop columns from view"}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id AS renamed,txt FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change name of view column \"id\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id::BIGINT AS id,txt FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"id\" from integer to bigint"}},
		{"CREATE VIEW cv AS SELECT txt::VARCHAR(3) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt::VARCHAR(4) AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from character varying(3) to character varying(4)"}},
		{"CREATE VIEW cv AS SELECT id::NUMERIC(10,1) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT id::NUMERIC(10,2) AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from numeric(10,1) to numeric(10,2)"}},
		{"CREATE VIEW cv AS SELECT txt COLLATE \"C\" AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE \"POSIX\" AS x FROM t", fixture.Diagnostic{Code: "42P16", Message: "cannot change collation of view column \"x\" from \"C\" to \"POSIX\""}},
		{"", "DROP VIEW t", fixture.Diagnostic{Code: "42809", Message: "\"t\" is not a view", Hint: "Use DROP TABLE to remove a table."}},
		{"", "DROP TABLE v", fixture.Diagnostic{Code: "42809", Message: "\"v\" is not a table", Hint: "Use DROP VIEW to remove a view."}},
		{"", "ALTER VIEW v SET(unknown_option=true)", fixture.Diagnostic{Code: "22023", Message: "unrecognized parameter \"unknown_option\""}},
		{"", "ALTER VIEW v SET(security_invoker=wat)", fixture.Diagnostic{Code: "22023", Message: "invalid value for boolean option \"security_invoker\": wat"}},
		{"", "ALTER VIEW v SET(security_invoker=true,security_invoker=false)", fixture.Diagnostic{Code: "22023", Message: "parameter \"security_invoker\" specified more than once"}},
		{"", "ALTER VIEW v RESET(security_invoker=true)", fixture.Diagnostic{Code: "42601", Message: "RESET must not include values for parameters"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				db := backend.DB
				for _, q := range []string{"CREATE TABLE t(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "CREATE VIEW v AS SELECT id,txt FROM t"} {
					fixture.UpdateReturningReferenceExec(t, db, q)
				}
				if tc.setup != "" {
					fixture.UpdateReturningReferenceExec(t, db, tc.setup)
				}
				_, err := db.Exec(tc.query)
				fixture.UpdateReturningReferenceDiagnostic(t, err, tc.want)
				rows, err := db.Query("SELECT * FROM v")
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "txt"}, Types: []string{"INT4", "TEXT"}, Rows: [][]any{{int64(1), "a"}}, Bag: false})
			}
		})
	}
}

func TestReferenceViewReplaceBPCharDiagnosticsConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, oldBPChar := range []bool{false, true} {
			t.Run(fmt.Sprintf("old_bpchar=%v/prepared=%v", oldBPChar, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					db := backend.DB
					oldExpression, newExpression, oldType, newType := "txt", "txt::bpchar", "text", "bpchar"
					if oldBPChar {
						oldExpression, newExpression, oldType, newType = newExpression, oldExpression, newType, oldType
					}
					for _, q := range []string{"CREATE TABLE t(txt TEXT)", "INSERT INTO t VALUES('a')", "CREATE VIEW v AS SELECT " + oldExpression + " AS x FROM t", "CREATE VIEW w AS SELECT x FROM v"} {
						fixture.UpdateReturningReferenceExec(t, db, q)
					}
					var before bytes.Buffer
					if !backend.PG {
						if err := stuntgres.Dump(context.Background(), p.Native, &before); err != nil {
							t.Fatal(err)
						}
					}
					query := "CREATE OR REPLACE VIEW v AS SELECT " + newExpression + " AS x FROM t"
					var err error
					if prepared {
						s, prepareErr := db.Prepare(query)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer func() {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
						}()
						_, err = s.Exec()
					} else {
						_, err = db.Exec(query)
					}
					fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42P16", Message: "cannot change data type of view column \"x\" from " + oldType + " to " + newType})
					if !backend.PG {
						var after bytes.Buffer
						if err := stuntgres.Dump(context.Background(), p.Native, &after); err != nil {
							t.Fatal(err)
						}
						if before.String() != after.String() {
							t.Fatal("failed replacement changed persistent definition")
						}
					}
					rows, err := db.Query("SELECT x::TEXT FROM v")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"x"}, Types: []string{"TEXT"}, Rows: [][]any{{"a"}}, Bag: false})
					rows, err = db.Query("SELECT x::TEXT FROM w")
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"x"}, Types: []string{"TEXT"}, Rows: [][]any{{"a"}}, Bag: false})
				}
			})
		}
	}
}
