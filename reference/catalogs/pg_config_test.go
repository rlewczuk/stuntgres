package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const pgConfigSelect = "SELECT name,setting FROM pg_catalog.pg_config"

var pgConfigColumns = []string{"name", "setting"}
var pgConfigTypes = []string{"TEXT", "TEXT"}

// Independently specified upstream key inventory, in ORDER BY name order.
var pgConfigKeys = []string{
	"BINDIR", "CC", "CFLAGS", "CFLAGS_SL", "CONFIGURE", "CPPFLAGS",
	"DOCDIR", "HTMLDIR", "INCLUDEDIR", "INCLUDEDIR-SERVER", "LDFLAGS",
	"LDFLAGS_EX", "LDFLAGS_SL", "LIBDIR", "LIBS", "LOCALEDIR", "MANDIR",
	"PGXS", "PKGINCLUDEDIR", "PKGLIBDIR", "SHAREDIR", "SYSCONFDIR", "VERSION",
}

func pgConfigVersion(b fixture.Backend) [][]any {
	version := "PostgreSQL 18.0 compatible; embedded stuntgres"
	if b.PG {
		version = "PostgreSQL 18.6 (Debian 18.6-1.pgdg12+2)"
	}
	return [][]any{{"VERSION", version}}
}

func pgConfigScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgConfigStep(query string, want [][]any, args ...any) fixture.Step {
	return fixture.Select(query, pgConfigColumns, pgConfigTypes, want, args...)
}

func pgConfigRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, pgConfigStep(pgConfigSelect+" WHERE name='VERSION'", pgConfigVersion(b)))
}

func pgConfigSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT n FROM pgconfig_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
}

func TestReferencePGConfigValues(t *testing.T) {
	pgConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{pgConfigSelect, "SELECT * FROM pg_config"} {
				want := pgConfigVersion(b)
				if b.PG {
					want = pgConfigPGValues()
				}
				s := pgConfigStep(query+" ORDER BY name", want)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			for _, suffix := range []string{" WHERE name='VERSION'", " WHERE name='version'", " WHERE name='STUNTGRES_UNKNOWN_BUILD_KEY'", " WHERE FALSE"} {
				var want [][]any
				if suffix == " WHERE name='VERSION'" {
					want = pgConfigVersion(b)
				}
				s := pgConfigStep(pgConfigSelect+suffix, want)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		conn, other := fixture.LockConnections(t, p.Ctx, b.DB)
		pgConfigRead(t, p, b, conn)
		pgConfigRead(t, p, b, other)
		plain := pgConfigStep(pgConfigSelect+" WHERE name='VERSION'", pgConfigVersion(b))
		plain.NonContext = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, plain)
		query := pgConfigSelect + " WHERE name=$1"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		for _, key := range []any{"VERSION", "version", "STUNTGRES_UNKNOWN_BUILD_KEY", nil, "VERSION"} {
			var want [][]any
			if key == "VERSION" {
				want = pgConfigVersion(b)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgConfigStep(query, want, key))
		}
		for i, key := range pgConfigKeys[:len(pgConfigKeys)-1] {
			var want [][]any
			if b.PG {
				want = [][]any{pgConfigPGValues()[i]}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgConfigStep(query, want, key))
		}
	})
}
