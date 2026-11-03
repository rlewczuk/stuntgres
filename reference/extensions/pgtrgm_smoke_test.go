package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmFeatureSmoke(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		check := pgTrgmStored(t, p, b, c)
		run := pgTrgmRun(t, p, b, c)
		getSQL := "SELECT trgm_addon.show_limit() AS threshold"
		getStmt, err := c.PrepareContext(p.Ctx, getSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer getStmt.Close()
		getHeld := fixture.HeldStatement{Executor: c, Statement: getStmt, SQL: getSQL}
		setSQL := "SELECT trgm_addon.set_limit($1::real) AS threshold"
		setStmt, err := c.PrepareContext(p.Ctx, setSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer setStmt.Close()
		setHeld := fixture.HeldStatement{Executor: c, Statement: setStmt, SQL: setSQL}
		get := func(want float32) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, getHeld, b.Recorder, pgTrgmScalar(b, getSQL, "threshold", "FLOAT4", pgTrgmFloat(b.PG, want)))
		}
		// Preparing both APIs is observational, not assignment.
		get(.3)
		distanceFrame := pgTrgmDistanceFrame(b)
		distanceStmt, err := c.PrepareContext(p.Ctx, distanceFrame.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer distanceStmt.Close()
		distanceHeld := fixture.HeldStatement{Executor: c, Statement: distanceStmt, SQL: distanceFrame.Query}
		run(distanceFrame)
		run(fixture.Prepared(distanceFrame))
		fixture.RunStep(t, p.Ctx, distanceHeld, b.Recorder, distanceFrame)
		q := "SELECT trgm_addon.show_trgm($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		array := pgTrgmArray("  c", " ca", "at ", "cat")
		run(pgTrgmScalar(b, "SELECT trgm_addon.show_trgm('cat')", "show_trgm", "_TEXT", array))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "show_trgm", "_TEXT", array, "cat"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "show_trgm", "_TEXT", nil, nil))
		run(pgTrgmSelect(b,
			"SELECT trgm_addon.similarity('a','aa'),trgm_addon.word_similarity('word','two words'),trgm_addon.strict_word_similarity('word','two words'),trgm_addon.similarity('café','caf') AS composed,trgm_addon.similarity('é','é') AS nonword,trgm_addon.similarity(NULL::text,'cat') AS absent",
			[]string{"similarity", "word_similarity", "strict_word_similarity", "composed", "nonword", "absent"},
			[]string{"FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4"},
			[][]any{{float64(0.25), pgTrgmReal(b.PG, 4, 5), pgTrgmReal(b.PG, 4, 7), float64(1), float64(0), nil}},
		))
		for _, threshold := range []string{"0", "1"} {
			for _, setting := range pgTrgmSettings {
				run(fixture.Exec("SET "+setting.name+"="+threshold, 0))
			}
			left, right := "'cat'", "'cat'"
			if threshold == "0" {
				left, right = "'!!!'", "''"
			}
			parts, names, types := []string{}, []string{}, []string{}
			values := []any{}
			for _, op := range pgTrgmOperators {
				parts = append(parts, left+" OPERATOR(trgm_addon."+op.token+") "+right+" AS \""+op.routine+"\"")
				names = append(names, op.routine)
				types = append(types, op.result)
				var value any = true
				if op.result == "FLOAT4" {
					value = float64(0)
					if threshold == "0" {
						value = float64(1)
					}
				}
				values = append(values, value)
			}
			run(pgTrgmSelect(b, "SELECT "+strings.Join(parts, ","), names, types, [][]any{values}))
			fixture.RunStep(t, p.Ctx, distanceHeld, b.Recorder, distanceFrame)
		}
		for _, setting := range pgTrgmSettings {
			run(fixture.Exec("RESET "+setting.name, 0))
		}
		boolSQL := "SELECT $1::text OPERATOR(trgm_addon.%) $2::text AS result"
		boolStmt, err := c.PrepareContext(p.Ctx, boolSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer boolStmt.Close()
		heldBool := fixture.HeldStatement{Executor: c, Statement: boolStmt, SQL: boolSQL}
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", false, "a", "aa"))
		run(fixture.Exec("SET pg_trgm.similarity_threshold='0.25'", 0))
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", true, "a", "aa"))
		run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", false, "a", "aa"))
		fixture.RunStep(t, p.Ctx, setHeld, b.Recorder, pgTrgmScalar(b, setSQL, "threshold", "FLOAT4", float64(.25), float64(.25)))
		get(.25)
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", true, "a", "aa"))
		fixture.RunStep(t, p.Ctx, setHeld, b.Recorder, pgTrgmScalar(b, setSQL, "threshold", "FLOAT4", nil, nil))
		get(.25)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL pg_trgm.similarity_threshold=1", 0))
		get(1)
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", false, "a", "aa"))
		run(fixture.Exec("ROLLBACK", 0))
		get(.25)
		fixture.RunStep(t, p.Ctx, heldBool, b.Recorder, pgTrgmScalar(b, boolSQL, "result", "BOOL", true, "a", "aa"))
		fixture.RunStep(t, p.Ctx, setHeld, b.Recorder, pgTrgmFailure(setSQL, fixture.Diagnostic{
			Code: "22023", Message: `2 is outside the valid range for parameter "pg_trgm.similarity_threshold" (0 .. 1)`,
		}, float64(2)))
		get(.25)
		var peerNotices []fixture.Diagnostic
		_, peer, recorder := fixture.NoticeReferencePool(t, p, b.PG, &peerNotices)
		peerBackend := b
		peerBackend.Recorder = recorder
		peerRun := pgTrgmRun(t, p, peerBackend, peer)
		if fixture.SettingsPhysical(t, c) == fixture.SettingsPhysical(t, peer) {
			t.Fatal("smoke reused the setter's physical owner")
		}
		peerRun(pgTrgmScalar(peerBackend, getSQL, "threshold", "FLOAT4", pgTrgmFloat(b.PG, .3)))
		peerRun(pgTrgmScalar(peerBackend, "SELECT 'a' OPERATOR(trgm_addon.%) 'aa' AS result", "result", "BOOL", false))
		pgTrgmNotices(t, &peerNotices)
		run(fixture.Exec("SET extra_float_digits='-5'", 0))
		// Observe installation through TEXT while transport precision is low,
		// then restore precision before asserting the getter's typed REAL.
		run(pgTrgmScalar(b, "SELECT trgm_addon.set_limit(0.26::real)", "set_limit", "FLOAT4", pgTrgmFloat(b.PG, .3)))
		pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.3")
		run(fixture.Exec("RESET extra_float_digits", 0))
		get(.3)
		run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
		for _, op := range pgTrgmOperators {
			if op.result != "BOOL" || op.kernel == 0 {
				continue
			}
			predicateSQL := "SELECT $1::text OPERATOR(trgm_addon." + op.token + ") $2::text AS predicate,trgm_addon." + op.routine + "($1,$2) AS backing"
			predicateStmt, err := c.PrepareContext(p.Ctx, predicateSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer predicateStmt.Close()
			predicate := fixture.HeldStatement{Executor: c, Statement: predicateStmt, SQL: predicateSQL}
			checkPredicate := func(left, right string, want bool) {
				t.Helper()
				if op.reverse {
					left, right = right, left
				}
				s := pgTrgmSelect(b, predicateSQL, []string{"predicate", "backing"}, []string{"BOOL", "BOOL"}, [][]any{{want, want}}, left, right)
				run(s)
				fixture.RunStep(t, p.Ctx, predicate, b.Recorder, s)
			}
			setting := pgTrgmSettings[op.kernel].name
			run(fixture.Exec("SET "+setting+"='1'", 0))
			checkPredicate("a", strings.Repeat("a ", 64), true)
			run(fixture.Exec("SET "+setting+"='0.5'", 0))
			// Arbitrary extent1/2 qualifies; complete-word extent1/4 does not.
			checkPredicate("a", strings.Repeat("aa ", 64), op.kernel == 1)
			run(fixture.Exec("SET "+setting+"='0.5000000000000001'", 0))
			checkPredicate("a", strings.Repeat("aa ", 64), false)
			run(fixture.Exec("RESET "+setting, 0))
			checkPredicate("a", strings.Repeat("aa ", 64), false)
		}
		run(fixture.Exec("SET pg_trgm.word_similarity_threshold='0.25'", 0))
		run(fixture.Exec("SET pg_trgm.strict_word_similarity_threshold='0.25'", 0))
		laterSQL := "SELECT trgm_addon.word_similarity($1,$2) AS word,trgm_addon.strict_word_similarity($1,$2) AS strict,trgm_addon.word_similarity_dist_op($1,$2) AS forward,trgm_addon.word_similarity_dist_commutator_op($2,$1) AS reverse,trgm_addon.strict_word_similarity_dist_op($1,$2) AS strict_forward,trgm_addon.strict_word_similarity_dist_commutator_op($2,$1) AS strict_reverse"
		later := pgTrgmSelect(b, laterSQL,
			[]string{"word", "strict", "forward", "reverse", "strict_forward", "strict_reverse"},
			[]string{"FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4"},
			[][]any{{float64(1), float64(1), float64(0), float64(0), float64(0), float64(0)}},
			"word", "two words word")
		run(later)
		run(fixture.Prepared(later))
		run(fixture.Exec("RESET pg_trgm.word_similarity_threshold", 0))
		run(fixture.Exec("RESET pg_trgm.strict_word_similarity_threshold", 0))
		// Directional representative: arbitrary and strict extents differ.
		distances := pgTrgmDistanceValues(b)
		run(pgTrgmSelect(b,
			"SELECT 'word' OPERATOR(trgm_addon.<<->) 'two words' AS forward,'word' OPERATOR(trgm_addon.<->>) 'two words' AS reverse,'word' OPERATOR(trgm_addon.<<<->) 'two words' AS strict_forward,'word' OPERATOR(trgm_addon.<->>>) 'two words' AS strict_reverse",
			[]string{"forward", "reverse", "strict_forward", "strict_reverse"}, []string{"FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4"},
			[][]any{{distances[1], distances[2], distances[3], distances[4]}},
		))
		if !b.PG {
			pgTrgmControls(t, p, b, c, check, false)
		}
		fixture.RunStep(t, p.Ctx, distanceHeld, b.Recorder, distanceFrame)
		pgTrgmNotices(t, notices)
	})
}
