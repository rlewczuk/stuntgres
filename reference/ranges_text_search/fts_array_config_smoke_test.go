package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsArrayConfigCancelCalls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, args ...any) fixture.HeldStatement {
	t.Helper()
	h := textSearchSettingHold(t, p, c, query)
	// Only native promises an ordinary context error with no cursor. Do not
	// assert pq pre-cancellation races or transport state as SQL semantics.
	if !b.PG {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, e := range []fixture.Executor{c, h} {
			rows, err := e.QueryContext(ctx, query, args...)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled function published a cursor")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("query cancellation crossed SQL boundary", err)
			}
			result, err := e.ExecContext(ctx, query, args...)
			if result != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatal("exec cancellation published a result or SQL error", result, err)
			}
		}
	}
	return h
}

func TestReferenceFTSArrayConfigCancellation(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ftsArrayConfigInstall(t, p, b, c)
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{"SELECT array_to_tsvector($1::text[]) AS result", "TSVECTOR", "'a' 'b'", []any{"{b,a,a}"}},
			{"SELECT get_current_ts_config() AS result", "REGCONFIG", "array_cfg_owned", nil},
		} {
			h := ftsArrayConfigCancelCalls(t, p, b, c, tc.q, tc.args...)
			defer h.Statement.Close()
			s := ftsArrayConfigValue(tc.q, tc.typ, tc.want, tc.args...)
			ftsArrayConfigBoth(t, p, b, c, s)
			ftsArrayConfigRun(t, p, b, h, s)
		}
		insert := ftsArrayConfigCancelCalls(t, p, b, c, "INSERT INTO public.array_cfg_saved(id,a,v) VALUES(2,$1::text[],array_to_tsvector($1::text[]))", "{b,a,a}")
		defer insert.Statement.Close()
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigRun(t, p, b, insert, fixture.Exec(insert.SQL, 1, "{c,c}"))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT v FROM public.array_cfg_saved WHERE id=2", []string{"v"}, []string{"TSVECTOR"}, [][]any{{"'c'"}}))
	})
}

func TestReferenceFTSArrayConfigFeatureSmoke(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigRawStep(b, fixture.Select("SELECT array_to_tsvector(ARRAY['b','a','a']) AS v", []string{"v"}, []string{"TSVECTOR"}, [][]any{{"'a' 'b'"}})))
		q := "SELECT array_to_tsvector($1::text[]) AS v"
		h := textSearchSettingHold(t, p, c, q)
		defer h.Statement.Close()
		for i, tc := range []struct{ input, want any }{{"{b,a,a}", "'a' 'b'"}, {nil, nil}, {"{}", ""}, {"{b,a,a}", "'a' 'b'"}} {
			ftsArrayConfigRun(t, p, b, h, ftsArrayConfigRawStep(b, fixture.Select(q, []string{"v"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.input)))
			if i == 0 {
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22004", "lexeme array may not contain nulls"), "{a,NULL}")
				s.SelectRows, s.ErrorStage = true, "execute"
				ftsArrayConfigRun(t, p, b, h, s)
			}
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer ftsConvertRestore(t, p, peer)()
		ftsArrayConfigRun(t, p, b, peer, fixture.Exec("SET default_text_search_config='simple'", 0))
		current := textSearchSettingHold(t, p, c, "SELECT get_current_ts_config() AS cfg")
		defer current.Statement.Close()
		englishOID := ftsArrayConfigOID(t, p, c, "pg_catalog", "english")
		for _, tc := range []struct {
			name string
			oid  int64
		}{{"simple", 3748}, {"english", englishOID}} {
			ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='"+tc.name+"'", 0))
			ftsArrayConfigRun(t, p, b, current, ftsArrayConfigRawStep(b, fixture.Select(current.SQL, []string{"cfg"}, []string{"REGCONFIG"}, [][]any{{tc.name}})))
			ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigCurrent(tc.name, tc.oid))
			ftsArrayConfigBoth(t, p, b, peer, ftsArrayConfigCurrent("simple", 3748))
		}
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET LOCAL default_text_search_config='simple'", 0))
		ftsArrayConfigRun(t, p, b, current, fixture.Select(current.SQL, []string{"cfg"}, []string{"REGCONFIG"}, [][]any{{"simple"}}))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		ftsArrayConfigRun(t, p, b, current, fixture.Select(current.SQL, []string{"cfg"}, []string{"REGCONFIG"}, [][]any{{"english"}}))
		ftsArrayConfigInstall(t, p, b, c)
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigAtomicity(t, p, b, c)
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='english'", 0))
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigStored())
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigView("english"))
		if !b.PG {
			ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='simple'", 0))
			ftsArrayConfigControls(t, p, b, c)
		}
		canceledVector := ftsArrayConfigCancelCalls(t, p, b, c, q, "{b,a,a}")
		defer canceledVector.Statement.Close()
		canceledCurrent := ftsArrayConfigCancelCalls(t, p, b, c, current.SQL)
		defer canceledCurrent.Statement.Close()
		ftsArrayConfigRun(t, p, b, canceledVector, fixture.Select(q, []string{"v"}, []string{"TSVECTOR"}, [][]any{{"'a' 'b'"}}, "{b,a,a}"))
		ftsArrayConfigRun(t, p, b, canceledCurrent, fixture.Select(current.SQL, []string{"cfg"}, []string{"REGCONFIG"}, [][]any{{"english"}}))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("INSERT INTO public.array_cfg_saved(id,a) VALUES(2,'{z,z}')", 1))
		ftsArrayConfigBoth(t, p, b, c, fixture.Select("SELECT v,cfg FROM public.array_cfg_saved WHERE id=2", []string{"v", "cfg"}, []string{"TSVECTOR", "REGCONFIG"}, [][]any{{"'a' 'b'", "english"}}))
	})
}
