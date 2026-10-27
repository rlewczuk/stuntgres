package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerOtherLifecycle(t *testing.T) {
	for _, d := range plannerOtherSettings {
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				peer := s.lockPeer()
				query := "SELECT current_setting(" + physicalLiteral(d.name) + ")"
				held, err := s.conn.PrepareContext(s.p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				check := func(value, source string) {
					s.resourceValue(d.name, value, value, source, true)
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: query}, s.recorder, clientText(query, "current_setting", value))
					peer.resourceValue(d.name, d.boot, d.boot, "default", false)
				}
				for _, prepared := range []bool{false, true} {
					command := func(q string) { step := fixture.Exec(q, 0); step.Prepared = prepared; s.run(step) }
					set := "SET " + d.name + "=" + physicalLiteral(d.first)
					local := "SET LOCAL " + d.name + "=" + physicalLiteral(d.second)
					for _, end := range []string{"COMMIT", "ROLLBACK"} {
						command("BEGIN")
						command(set)
						check(d.first, "session")
						command(end)
						if end == "COMMIT" {
							check(d.first, "session")
						} else {
							check(d.boot, "default")
						}
						command("RESET " + d.name)
						check(d.boot, "default")
						command("BEGIN")
						command(local)
						check(d.second, "session")
						command(end)
						check(d.boot, "default")
					}
					command("BEGIN")
					command(set)
					command(local)
					check(d.second, "session")
					command("COMMIT")
					check(d.first, "session")
					command("RESET " + d.name)
					command("BEGIN")
					command(set)
					command(local)
					command("ROLLBACK")
					check(d.boot, "default")
					command("BEGIN")
					command(set)
					command("SAVEPOINT other_outer")
					command(local)
					check(d.second, "session")
					command("SAVEPOINT other_inner")
					command(set)
					check(d.first, "session")
					command("ROLLBACK TO other_inner")
					check(d.second, "session")
					command("RELEASE other_inner")
					command("ROLLBACK TO other_outer")
					check(d.first, "session")
					command("RELEASE other_outer")
					command("ROLLBACK")
					check(d.boot, "default")
					for _, localFlag := range []bool{false, true} {
						command("BEGIN")
						step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{d.first}}, d.name, d.first, localFlag)
						step.Prepared = prepared
						s.run(step)
						check(d.first, "session")
						command("COMMIT")
						if localFlag {
							check(d.boot, "default")
						} else {
							check(d.first, "session")
						}
						command("RESET " + d.name)
					}
					for _, reset := range []string{"SET " + d.name + "=DEFAULT", "RESET " + d.name, "RESET ALL", "SELECT set_config($1,NULL,false)"} {
						command(set)
						if reset == "SELECT set_config($1,NULL,false)" {
							step := fixture.Select(reset, []string{"set_config"}, []string{"TEXT"}, [][]any{{d.boot}}, d.name)
							step.Prepared = prepared
							s.run(step)
						} else {
							command(reset)
						}
						check(d.boot, "default")
					}
				}
			})
		})
	}
}
