package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLDiagnostics(t *testing.T) {
	cases := []struct {
		name, query string
		want        fixture.Diagnostic
		syntax      bool
	}{
		{"missing_state", "ALTER EVENT TRIGGER et_absent ENABLE ALWAYS", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`), false},
		{"missing_rename", "ALTER EVENT TRIGGER et_absent RENAME TO et_new", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`), false},
		{"missing_owner", "ALTER EVENT TRIGGER et_absent OWNER TO CURRENT_USER", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`), false},
		{"missing_drop", "DROP EVENT TRIGGER et_absent", eventTriggerDiagnostic("42704", `event trigger "et_absent" does not exist`), false},
		{"missing_role_first", "ALTER EVENT TRIGGER et_absent OWNER TO missing_owner", eventTriggerDiagnostic("42704", `role "missing_owner" does not exist`), false},
		{"missing_role_existing", "ALTER EVENT TRIGGER et OWNER TO missing_owner", eventTriggerDiagnostic("42704", `role "missing_owner" does not exist`), false},
		{"self_rename", "ALTER EVENT TRIGGER et RENAME TO et", eventTriggerDiagnostic("42710", `event trigger "et" already exists`), false},
	}
	for _, invalid := range []struct{ query, token string }{
		{"ALTER EVENT TRIGGER et ENABLE BOGUS", "BOGUS"},
		{"ALTER EVENT TRIGGER et DISABLE REPLICA", "REPLICA"},
		{"ALTER EVENT TRIGGER et DISABLE ALWAYS", "ALWAYS"},
		{"ALTER EVENT TRIGGER et ENABLE ORIGIN", "ORIGIN"},
		{"ALTER EVENT TRIGGER et RENAME TO", ""},
		{"ALTER EVENT TRIGGER public.et ENABLE", "."},
		{"ALTER EVENT TRIGGER et ENABLE ALWAYS junk", "junk"},
		{"ALTER EVENT TRIGGER et ENABLE REPLICA junk", "junk"},
	} {
		message := "syntax error at end of input"
		position := len(invalid.query) + 1
		if invalid.token != "" {
			message = `syntax error at or near "` + invalid.token + `"`
			position = strings.Index(invalid.query, invalid.token) + 1
		}
		want := eventTriggerDiagnostic("42601", message)
		want.Position = strconv.Itoa(position)
		cases = append(cases, struct {
			name, query string
			want        fixture.Diagnostic
			syntax      bool
		}{invalid.query, invalid.query, want, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
				eventTriggerScenario(t, []string{"et", "et_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					d := eventDDLCase{t, p, b, c, route}
					d.seed("et", "ddl_command_end", "'ALTER TABLE'")
					i := d.identity("et", "ddl_command_end", "O", `{"ALTER TABLE"}`)
					d.failure(tc.query, tc.want, tc.syntax)
					d.catalog(c, i)
					d.dependencies(i, true)
					d.failure("DROP FUNCTION et_ddl_fn() RESTRICT", eventDDLFunctionDependency("et_ddl_fn", "et"), false)
					d.audit()
					d.utility("ALTER TABLE et_ddl_data ALTER COLUMN id SET DEFAULT 7")
					d.audit(eventTriggerRow(1, "et_ddl_fn", "ddl_command_end", "ALTER TABLE"))
					d.run(fixture.Select("SELECT id FROM et_ddl_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		})
	}
}
