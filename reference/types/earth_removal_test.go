package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthPrerequisiteRemoval(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			installConsumer := func() {
				earthInstall(t, p, b, c)
				run(fixture.Exec("CREATE TABLE earth_removal(id integer NOT NULL,v earth)", 0))
				run(fixture.Exec("INSERT INTO earth_removal VALUES (1,'(6378168,0)'),(2,NULL)", 2))
			}
			verify := func() {
				run(fixture.Select("SELECT extname,extversion FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname", "extversion"}, []string{"NAME", "TEXT"}, [][]any{{"cube", "1.5"}, {"earthdistance", "1.2"}}))
				s := fixture.EarthSelect(b, "SELECT id,v FROM earth_removal ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(6378168, 0)"}, {int64(2), nil}})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
				run(s)
				run(fixture.Prepared(s))
				earthIdentityChecks(t, p, b, c, b.Recorder)
			}
			identities := func() [4]int64 {
				var ids [4]int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'cube'::regtype::oid,'earth'::regtype::oid,(SELECT oid FROM pg_extension WHERE extname='cube'),(SELECT oid FROM pg_extension WHERE extname='earthdistance')").Scan(&ids[0], &ids[1], &ids[2], &ids[3]); err != nil {
					t.Fatal(err)
				}
				for _, id := range ids {
					if id <= 0 {
						t.Fatalf("missing installed identity: %v", ids)
					}
				}
				return ids
			}
			installConsumer()
			verify()
			before := identities()
			// Complete portable diagnostic observed by root on pinned18.6.
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop extension cube because other objects depend on it")
			want.Detail = "extension earthdistance depends on extension cube\ncolumn v of table earth_removal depends on type earth"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			s := fixture.ErrorDiagnostic("DROP EXTENSION cube RESTRICT", want)
			s.ErrorStage = "execute"
			run(s)
			if after := identities(); after != before {
				t.Fatalf("failed RESTRICT changed installed identities: %v -> %v", before, after)
			}
			verify()
			// Exercise ordinary SQL removal, not a native replacement control.
			for _, q := range []string{"DROP TABLE earth_removal", "DROP EXTENSION earthdistance", "DROP EXTENSION cube"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname", []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('cube','_cube','earth','_earth') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
			installConsumer()
			verify()
		})
	}
}
