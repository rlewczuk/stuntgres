package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionDependenciesInstallation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer c.Close()
					b.Recorder = recorder
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
					s := fixture.Exec("CREATE EXTENSION earthdistance VERSION '1.2' CASCADE", 0)
					s.Prepared = prepared
					run(s)
					wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `installing required extension "cube"`}
					if !reflect.DeepEqual(notices, []fixture.Diagnostic{wantNotice}) {
						t.Fatalf("required-install notices %#v want %#v", notices, wantNotice)
					}
					dependencyGraph(t, p, b, c, recorder)
					before := dependencyCaptureCube(t, p, c)
					run(fixture.Exec("CREATE TABLE closure_values(id integer NOT NULL,v earth)", 0))
					run(fixture.Exec("CREATE TABLE closure_empty(v earth NOT NULL)", 0))
					run(fixture.Exec("INSERT INTO closure_values VALUES (1,'(6378168,0)'),(2,NULL)", 2))
					insert := earthHeld(t, p, c, "INSERT INTO closure_values VALUES ($1,$2)")
					defer insert.Statement.Close()
					fixture.RunStep(t, p.Ctx, insert, recorder, fixture.Exec(insert.SQL, 1, 3, "(6378168,0,0)"))
					fixture.RunStep(t, p.Ctx, insert, recorder, fixture.Exec(insert.SQL, 1, 4, nil))
					for _, tc := range []struct {
						q        string
						rows     [][]any
						nullable bool
					}{
						{"SELECT v FROM closure_values ORDER BY id", [][]any{{"(6378168, 0)"}, {nil}, {"(6378168, 0, 0)"}, {nil}}, true},
						{"SELECT v FROM closure_values WHERE v IS NULL ORDER BY id", [][]any{{nil}, {nil}}, true},
						{"SELECT v FROM closure_empty", [][]any{}, false},
					} {
						s := fixture.EarthSelect(b, tc.q, []string{"v"}, tc.rows)
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, []string{"v"}, []bool{tc.nullable}) }
						run(s)
						run(fixture.Prepared(s))
						held := earthHeld(t, p, c, tc.q)
						fixture.RunStep(t, p.Ctx, held, recorder, s)
						fixture.RunStep(t, p.Ctx, held, recorder, s)
						if err := held.Statement.Close(); err != nil {
							t.Fatal(err)
						}
					}
					capabilities := fixture.Select("SELECT cube_dim('(1,2)'::cube) AS dimensions,earth() AS radius,latitude(ll_to_earth(0,0)) AS latitude,geo_distance(point '(0,0)',point '(0,0)') AS distance", []string{"dimensions", "radius", "latitude", "distance"}, []string{"INT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(2), float64(6378168), float64(0), float64(0)}})
					run(capabilities)
					run(fixture.Prepared(capabilities))
					dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance", prepared, fixture.ControlsReferenceDiagnostic("42710", `extension "earthdistance" already exists`))
					for _, q := range []string{"CREATE EXTENSION IF NOT EXISTS earthdistance", "CREATE EXTENSION IF NOT EXISTS earthdistance SCHEMA missing_schema VERSION 'missing' CASCADE"} {
						notices = nil
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						run(s)
						want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "earthdistance" already exists, skipping`}
						if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
							t.Fatalf("existing notice %#v want %#v", notices, want)
						}
						dependencyUnchangedCube(t, p, c, before)
						dependencyGraph(t, p, b, c, recorder)
					}
				})
			}
		})
	}
}
