package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterArrayInputDemand(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE DOMAIN copy_product_domain AS int CHECK (10 / VALUE > 0)")
		for _, format := range []string{"text", "csv"} {
			t.Run(format, func(t *testing.T) {
				for _, tc := range []struct {
					name, typ string
					failure   copyFilterProviderFailure
					hard      bool
				}{
					{
						name: "hard_domain", typ: "copy_product_domain[]", hard: true,
						failure: copyFilterProviderFailure{bad: "[1:50000][1:50000]={{0}}", code: "22012", message: "division by zero"},
					},
					{
						name: "stop_child", typ: "int[]",
						failure: copyFilterProviderFailure{bad: "[1:50000][1:50000]={{bad}}", code: "22P02", message: `invalid input syntax for type integer: "bad"`},
					},
					{
						name: "mismatch", typ: "int[]",
						failure: copyFilterProviderFailure{bad: "[1:50000][1:50000]={{1}}", code: "22P02", message: `malformed array literal: "[1:50000][1:50000]={{1}}"`, detail: "Specified array dimensions do not match array contents."},
					},
					// These individual-declaration checks must stay before child
					// demand, even after the preceding product exceeds int32.
					{
						name: "declared_rank", typ: "copy_product_domain[]",
						failure: copyFilterProviderFailure{bad: "[1:50000][1:50000]" + strings.Repeat("[1:1]", 5) + "={{0}}", code: "54000", message: "number of array dimensions exceeds the maximum allowed (6)"},
					},
					{
						name: "declared_zero", typ: "copy_product_domain[]",
						failure: copyFilterProviderFailure{bad: "[1:50000][1:50000][1:0]={{0}}", code: "2202E", message: "upper bound cannot be less than lower bound"},
					},
				} {
					t.Run(tc.name, func(t *testing.T) {
						table := "copy_array_demand_" + format + "_" + tc.name
						c.exec(t, "CREATE TABLE "+table+"(id int,v "+tc.typ+")")
						defer c.exec(t, "DROP TABLE "+table)
						c.exec(t, "INSERT INTO "+table+" VALUES(99,'{{9}}')")
						// Canonical array text avoids catalog-specific domain-array
						// OIDs; COPY TO below also exports the actual typed column.
						query := "SELECT id,v::text AS v FROM " + table + " ORDER BY id"
						conn, held := copyFilterHeld(t, p, b, query)
						before := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "{{9}}"}})
						copyFilterConsumers(t, p, b, conn, held, before)
						input := copyFilterAliasRow(format, 1, "{{1}}") + copyFilterAliasRow(format, 2, tc.failure.bad) + copyFilterAliasRow(format, 3, "{{2}}")
						actions := []string{"stop", "ignore"}
						if tc.name == "stop_child" {
							actions = []string{"default_stop", "stop", "ignore"}
						}
						for _, action := range actions {
							t.Run(action, func(t *testing.T) {
								*notices = (*notices)[:0]
								options := "FORMAT " + format
								if action != "default_stop" {
									options += ",ON_ERROR " + action + ",LOG_VERBOSITY verbose"
								}
								if action == "ignore" {
									options += ",REJECT_LIMIT 1"
								}
								copySQL := "COPY " + table + " FROM STDIN WITH(" + options + ")"
								state := before
								output := "99\t{{9}}\n"
								count := int64(1)
								if action == "ignore" && !tc.hard {
									copyFilterImport(t, c, copySQL, input, 2)
									state.Rows = [][]any{{int64(1), "{{1}}"}, {int64(3), "{{2}}"}, {int64(99), "{{9}}"}}
									output = "1\t{{1}}\n3\t{{2}}\n" + output
									count = 3
									copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"`+tc.failure.bad+`"`, 2), copyFilterSummary(1))
								} else {
									copyFilterFailure(t, c, copySQL, input, tc.failure.diagnostic(table, 2))
									copyFilterNotices(t, *notices)
								}
								copyFilterConsumers(t, p, b, conn, held, state)
								c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", output, count)
							})
						}
					})
				}
			})
		}
	})
}
