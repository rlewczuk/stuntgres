package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsNullableUtilityIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		cleanup := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := s.observer.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS pgss_nullable_wrapper CASCADE"); err != nil {
				s.t.Error(err)
			}
		}
		cleanup()
		defer cleanup()
		s.exec("CREATE FOREIGN DATA WRAPPER pgss_nullable_wrapper NO HANDLER NO VALIDATOR")
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		base := "CREATE SERVER pgss_nullable_server"
		tail := " FOREIGN DATA WRAPPER pgss_nullable_wrapper"
		queries := []string{base + tail, base + " TYPE ''" + tail, base + " VERSION ''" + tail, base + " VERSION NULL" + tail}
		values := [][]any{{nil, nil}, {"", nil}, {nil, ""}, {nil, nil}}
		s.counts(base+"%", 0, 0, 0)
		for i, query := range queries {
			step := fixture.Exec(query, 0)
			if i%2 == 1 {
				step = fixture.Prepared(step)
			}
			s.work(step)
			s.both(fixture.Select("SELECT srvtype,srvversion FROM pg_foreign_server WHERE srvname='pgss_nullable_server'", []string{"srvtype", "srvversion"}, []string{"TEXT", "TEXT"}, [][]any{values[i]}))
			s.exec("DROP SERVER pgss_nullable_server")
		}
		// Nullable selected strings distinguish empty from absent; CREATE
		// VERSION NULL is absence, not ALTER's separate has_version flag.
		s.entry(queries[0], 2, 0)
		s.entry(queries[1], 1, 0)
		s.entry(queries[2], 1, 0)
		s.counts(queries[3], 0, 0, 0)
		s.counts(base+"%", 3, 4, 0)
		s.exec(queries[0])
		alterEmpty := "ALTER SERVER pgss_nullable_server VERSION ''"
		alterNull := "ALTER SERVER pgss_nullable_server VERSION NULL"
		s.work(fixture.Exec(alterEmpty, 0))
		s.work(fixture.Prepared(fixture.Exec(alterNull, 0)))
		s.entry(alterEmpty, 1, 0)
		s.entry(alterNull, 1, 0)
		options := "ALTER SERVER pgss_nullable_server OPTIONS (ADD k 'v')"
		versionOptions := "ALTER SERVER pgss_nullable_server VERSION NULL OPTIONS (ADD k 'v')"
		s.work(fixture.Exec(options, 0))
		s.exec("ALTER SERVER pgss_nullable_server OPTIONS (DROP k)")
		s.work(fixture.Prepared(fixture.Exec(versionOptions, 0)))
		s.entry(options, 1, 0)
		s.entry(versionOptions, 1, 0)
		s.both(fixture.Select("SELECT srvtype,srvversion,srvoptions[1] AS option FROM pg_foreign_server WHERE srvname='pgss_nullable_server'", []string{"srvtype", "srvversion", "option"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, "k=v"}}))
	})
}
