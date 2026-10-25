package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func issn13Scenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func issn13InstallUnpublished(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, routine string) {
	t.Helper()
	isbnInstallUnpublished(t, p, b, c, routine)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
}
