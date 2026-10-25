package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesDomainGraph(t *testing.T) {
	for _, carrier := range []string{"array", "composite", "domain_array"} {
		for _, action := range []string{"ADD CONSTRAINT positive CHECK(VALUE>0)", "VALIDATE CONSTRAINT positive", "SET NOT NULL"} {
			t.Run(carrier+"/"+action, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
						typ := "tt_d[]"
						if carrier == "composite" {
							run(fixture.Exec("CREATE TYPE tt_container AS(v tt_d)", 0))
							typ = "tt_container"
						}
						if carrier == "domain_array" {
							run(fixture.Exec("CREATE DOMAIN tt_container AS tt_d[]", 0))
							typ = "tt_container"
						}
						run(fixture.Exec("CREATE TABLE tt_holder(v "+typ+")", 0))
						run(fixture.Exec("INSERT INTO tt_holder VALUES(NULL)", 1))
						if action == "VALIDATE CONSTRAINT positive" {
							run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
						}
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("SAVEPOINT graph", 0))
						d := fixture.ControlsReferenceDiagnostic("0A000", `cannot alter type "tt_d" because column "tt_holder.v" uses it`)
						run(fixture.ErrorDiagnostic("ALTER DOMAIN tt_d "+action, d))
						run(fixture.Exec("ROLLBACK TO graph", 0))
						run(fixture.Exec("DROP TABLE tt_holder", 0))
						run(fixture.Exec("ALTER DOMAIN tt_d "+action, 0))
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT count(*) FROM tt_holder", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
	for _, storage := range []string{"derived", "partition", "materialized", "unstored"} {
		t.Run(storage, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE DOMAIN tt_d AS integer", 0))
					run(fixture.Exec("CREATE DOMAIN tt_child AS tt_d", 0))
					switch storage {
					case "derived":
						run(fixture.Exec("CREATE TABLE tt_scan(a tt_child,b tt_d)", 0))
						run(fixture.Exec("INSERT INTO tt_scan VALUES(1,-1)", 1))
					case "partition":
						run(fixture.Exec("CREATE TABLE tt_scan(id integer,v tt_child) PARTITION BY RANGE(id)", 0))
						run(fixture.Exec("CREATE TABLE tt_leaf PARTITION OF tt_scan FOR VALUES FROM(0) TO(10)", 0))
						run(fixture.Exec("INSERT INTO tt_scan VALUES(1,-1)", 1))
					case "materialized":
						run(fixture.Exec("CREATE MATERIALIZED VIEW tt_scan AS SELECT (-1)::tt_child AS v", 1))
					case "unstored":
						run(fixture.Exec("CREATE TYPE tt_unstored AS(v tt_d)", 0))
						run(fixture.Exec("CREATE VIEW tt_view AS SELECT NULL::tt_d AS v", 0))
					}
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT graph", 0))
					if storage == "unstored" {
						run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", 0))
					} else {
						table, column := "tt_scan", "v"
						if storage == "derived" {
							column = "b"
						}
						if storage == "partition" {
							table = "tt_leaf"
						}
						d := fixture.ControlsReferenceDiagnostic("23514", `column "`+column+`" of table "`+table+`" contains values that violate the new constraint`)
						d.Schema, d.Table, d.Column = "public", table, column
						run(fixture.ErrorDiagnostic("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", d))
						run(fixture.Exec("ROLLBACK TO graph", 0))
						run(fixture.Exec("ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
					}
					run(fixture.Exec("ROLLBACK", 0))
				})
			}
		})
	}
}
