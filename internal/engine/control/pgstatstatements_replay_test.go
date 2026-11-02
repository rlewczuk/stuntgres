package control

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Rendering is supplementary proof: public ConsumerLifecycle scenarios execute
// these commands and independently check the surviving ordinary view state.
func TestPgStatStatementsInstallationReplayConsumers(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	for _, schema := range []string{"__stuntgres_install_0", "retained_雪"} {
		t.Run(schema, func(t *testing.T) {
			recipe := catalog.NewInstallation(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 50000}, "pg_stat_statements", "1.12", extension.StockPgStatStatements, nil)
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, DomainRoles: source.DomainRoles}}
			item := replayInstallation{recipe: recipe, schema: schema, retainedSchema: true}
			schedule, err := script.newInstallationSchedule([]replayInstallation{item})
			if err != nil {
				t.Fatal(err)
			}
			if err := schedule.install(0); err != nil {
				t.Fatal(err)
			}
			text := strings.Join(script.installationStatements, "")
			qualified := catalog.QuoteIdentifier(schema) + "."
			want := "SET SESSION AUTHORIZATION \"postgres\";\n" +
				"CREATE EXTENSION \"pg_stat_statements\" WITH SCHEMA " + catalog.QuoteIdentifier(schema) + " VERSION '1.12';\n" +
				"SET SESSION AUTHORIZATION postgres;\n" +
				"ALTER EXTENSION \"pg_stat_statements\" DROP VIEW " + qualified + "\"pg_stat_statements_info\";\n" +
				"DROP VIEW " + qualified + "\"pg_stat_statements_info\";\n" +
				"ALTER EXTENSION \"pg_stat_statements\" DROP VIEW " + qualified + "\"pg_stat_statements\";\n" +
				"DROP VIEW " + qualified + "\"pg_stat_statements\";\n"
			if text != want {
				t.Fatalf("fresh script products must retire at their actual creation coordinates before function placement\ngot:\n%s\nwant:\n%s", text, want)
			}
			// Every emitted command must pass the same control admission checks
			// used by the root Restore hook table before ordinary SQL execution.
			statements, err := parser.Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			hooks := []Hook[struct{}]{
				{Name: "authorization", Admit: AdmitKind[struct{}, *parser.SessionAuthorization]},
				{Name: "extension", Admit: AdmitKind[struct{}, *parser.CreateExtension]},
				{Name: "membership", Admit: AdmitExtension[struct{}]},
				{Name: "consumer retirement", Admit: AdmitStatement[struct{}](CheckDropView)},
			}
			for _, statement := range statements {
				if answer, err := runAdmit(hooks, struct{}{}, nil, statement); err != nil || answer != Admitted {
					t.Fatal("installation prelude cannot reach normal replay execution", statement, answer, err)
				}
			}
			if !schedule.installed[0] || schedule.currentSchema[0] != schema || schedule.active[recipe.Provider] != 0 {
				t.Fatal("consumer retirement did not complete the same cohort", schedule.currentSchema)
			}
			if err := schedule.install(0); err != nil {
				t.Fatal(err)
			}
			if strings.Join(script.installationStatements, "") != want {
				t.Fatal("same cohort installed or retired consumers twice")
			}
		})
	}
}
