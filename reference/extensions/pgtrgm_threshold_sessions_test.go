package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmThresholdSessions(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, notices *[]fixture.Diagnostic) {
		runA := pgTrgmRun(t, p, b, a)
		runA(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		var peerNotices []fixture.Diagnostic
		_, peer, recorder := fixture.NoticeReferencePool(t, p, b.PG, &peerNotices)
		peerBackend := b
		peerBackend.Recorder = recorder
		runB := pgTrgmRun(t, p, peerBackend, peer)
		type sessionCheck struct {
			backend fixture.Backend
			getter  fixture.HeldStatement
			match   fixture.HeldStatement
		}
		var checks []sessionCheck
		for _, owner := range []struct {
			backend fixture.Backend
			conn    *sql.Conn
		}{{b, a}, {peerBackend, peer}} {
			getSQL := "SELECT show_limit() AS value"
			get, err := owner.conn.PrepareContext(p.Ctx, getSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer get.Close()
			matchSQL := "SELECT $1::text % $2::text AS match"
			match, err := owner.conn.PrepareContext(p.Ctx, matchSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer match.Close()
			checks = append(checks, sessionCheck{owner.backend,
				fixture.HeldStatement{Executor: owner.conn, Statement: get, SQL: getSQL},
				fixture.HeldStatement{Executor: owner.conn, Statement: match, SQL: matchSQL}})
		}
		check := func(index int, value float32, match bool) {
			owner := checks[index]
			fixture.RunStep(t, p.Ctx, owner.getter, owner.backend.Recorder,
				pgTrgmScalar(owner.backend, owner.getter.SQL, "value", "FLOAT4", pgTrgmFloat(owner.backend.PG, value)))
			fixture.RunStep(t, p.Ctx, owner.match, owner.backend.Recorder,
				pgTrgmScalar(owner.backend, owner.match.SQL, "match", "BOOL", match, "a", "aa"))
		}
		check(0, 0.3, false)
		check(1, 0.3, false)
		runA(pgTrgmScalar(b, "SELECT set_limit(0.25::real)", "set_limit", "FLOAT4", float64(0.25)))
		check(0, 0.25, true)
		check(1, 0.3, false)
		for _, setting := range pgTrgmSettings[1:] {
			pgTrgmSettingValue(runA, setting.name, setting.boot)
			pgTrgmSettingValue(runB, setting.name, setting.boot)
		}
		runB(pgTrgmScalar(peerBackend, "SELECT set_limit(1::real)", "set_limit", "FLOAT4", float64(1)))
		check(0, 0.25, true)
		check(1, 1, false)

		// Independent distinct-set scores for word,two words: 4/11 ordinary,
		// 4/5 forward word, 2/5 reverse word, 4/7 forward strict, 4/11 reverse.
		families := func(want [3][2]bool) {
			for _, op := range pgTrgmOperators {
				if op.result != "BOOL" {
					continue
				}
				for direction, pair := range [][2]string{{"word", "two words"}, {"two words", "word"}} {
					left, right := pair[0], pair[1]
					if op.reverse {
						left, right = right, left
					}
					q := "SELECT $1::text " + op.token + " $2::text AS match"
					s := pgTrgmScalar(b, q, "match", "BOOL", want[op.kernel][direction], left, right)
					runA(s)
					runA(fixture.Prepared(s))
					runA(pgTrgmScalar(b, "SELECT "+op.routine+"($1,$2) AS match", "match", "BOOL", want[op.kernel][direction], left, right))
				}
			}
		}
		for _, setting := range pgTrgmSettings {
			runA(fixture.Exec("SET "+setting.name+"='0.5'", 0))
		}
		baseline := [3][2]bool{{false, false}, {true, false}, {true, false}}
		families(baseline)
		runA(pgTrgmScalar(b, "SELECT set_limit(0.25::real)", "set_limit", "FLOAT4", float64(0.25)))
		families([3][2]bool{{true, true}, {true, false}, {true, false}})
		pgTrgmSettingValue(runA, pgTrgmSettings[1].name, "0.5")
		pgTrgmSettingValue(runA, pgTrgmSettings[2].name, "0.5")
		runA(fixture.Exec("SET pg_trgm.similarity_threshold='0.5'", 0))
		families(baseline)
		runA(fixture.Exec("SET pg_trgm.word_similarity_threshold='0.9'", 0))
		families([3][2]bool{{false, false}, {false, false}, {true, false}})
		runA(fixture.Exec("SET pg_trgm.word_similarity_threshold='0.5'", 0))
		families(baseline)
		runA(fixture.Exec("SET pg_trgm.strict_word_similarity_threshold='0.9'", 0))
		families([3][2]bool{{false, false}, {true, false}, {false, false}})
		runA(fixture.Exec("SET pg_trgm.strict_word_similarity_threshold='0.5'", 0))
		families(baseline)
		check(1, 1, false)
		if !b.PG {
			// Driver identity is a native API, distinct from two physical sessions.
			connector, err := stuntgres.NewDriver().OpenConnector("pgtrgm_threshold_independent")
			if err != nil {
				t.Fatal(err)
			}
			db, ownRecorder := fixture.RecordedNativePool(t, connector)
			independent, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer independent.Close()
			ownBackend := b
			ownBackend.Recorder = ownRecorder
			ownRun := pgTrgmRun(t, p, ownBackend, independent)
			ownRun(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			ownRun(pgTrgmScalar(ownBackend, "SELECT show_limit()", "show_limit", "FLOAT4", pgTrgmFloat(false, 0.3)))
			ownRun(pgTrgmScalar(ownBackend, "SELECT 'a' % 'aa' AS match", "match", "BOOL", false))
		}
		pgTrgmNotices(t, notices)
		pgTrgmNotices(t, &peerNotices)
	})
}
