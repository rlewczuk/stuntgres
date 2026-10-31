package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceYiddishSnowballPrepared(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ddl := "CREATE TEXT SEARCH DICTIONARY yiddish_prepared (TEMPLATE=snowball,Language=yiddish)"
		create, err := c.PrepareContext(p.Ctx, ddl)
		if err != nil { t.Fatal(err) }
		defer create.Close()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='yiddish_prepared'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: create, SQL: ddl}, b.Recorder, fixture.Exec(ddl, 0))
		const constantSQL = "SELECT ts_lexize('yiddish_prepared'::regdictionary,'ONLY'::text) AS lexemes"
		const parameterSQL = "SELECT ts_lexize($1,$2) AS lexemes"
		constant, err := c.PrepareContext(p.Ctx, constantSQL)
		if err != nil { t.Fatal(err) }
		defer constant.Close()
		parameter, err := c.PrepareContext(p.Ctx, parameterSQL)
		if err != nil { t.Fatal(err) }
		defer parameter.Close()
		ch := fixture.HeldStatement{Executor: c, Statement: constant, SQL: constantSQL}
		ph := fixture.HeldStatement{Executor: c, Statement: parameter, SQL: parameterSQL}
		for _, q := range []string{"PREPARE yiddish_named AS " + constantSQL, "PREPARE yiddish_params(regdictionary,text) AS SELECT ts_lexize($1,$2) AS lexemes", "PREPARE yiddish_inferred AS SELECT ts_lexize($1,$2) AS lexemes"} { regtsExec(t, p, b, c, q, 0) }
		defer regtsExec(t, p, b, c, "DEALLOCATE ALL", 0)
		for range 6 {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE yiddish_named", "{only}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "yiddish_prepared", "ONLY")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_prepared (stopwords=english)", 0)
		check := func() {
			dictionaryResult(t, p, b, ch, constantSQL, "{only}")
			dictionaryResult(t, p, b, c, "EXECUTE yiddish_named", "{only}")
			dictionaryResult(t, p, b, c, constantSQL, "{}")
			fresh, err := c.PrepareContext(p.Ctx, constantSQL)
			if err != nil { t.Fatal(err) }
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: fresh, SQL: constantSQL}, constantSQL, "{}")
			fresh.Close()
			for _, input := range []any{"ONLY", []byte("ONLY")} { dictionaryResult(t, p, b, ph, parameterSQL, "{}", "yiddish_prepared", input) }
			dictionaryResult(t, p, b, c, "EXECUTE yiddish_params('yiddish_prepared','ONLY')", "{}")
			dictionaryResult(t, p, b, c, "EXECUTE yiddish_inferred('yiddish_prepared','קינדער')", "{קינד}")
			dictionaryResult(t, p, b, ph, parameterSQL, "{קינד}", []byte("yiddish_prepared"), []byte("קינדער"))
			dictionaryResult(t, p, b, ph, parameterSQL, "{rats}", "yiddish_prepared", "RATS")
			dictionaryResult(t, p, b, ph, parameterSQL, "{only}", "yiddish_stem", "ONLY")
		}
		check()
		yiddishMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_prepared (language=english,stopwords=yiddish)")
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_prepared'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'yiddish', stopwords = 'english'"}})
		check()
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_prepared (language=english)", 0)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "yiddish_prepared", "RUNNING")
		dictionaryResult(t, p, b, ch, constantSQL, "{only}")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY yiddish_prepared (language=yiddish_bogus)", "42704", `no Snowball stemmer available for language "yiddish_bogus" and encoding "UTF8"`)
		dictionaryResult(t, p, b, ph, parameterSQL, "{run}", "yiddish_prepared", "RUNNING")
		for _, q := range []string{"SELECT to_tsvector($1,$2) AS normalized", "SELECT to_tsvector($1::regconfig,$2::text) AS normalized"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			for _, pair := range [][2]string{{"קינדער RATS 123", "'123':3 'rats':2 'קינד':1"}, {"ONLY", "'only':1"}, {"", ""}} {
				s := fixture.Select(q, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{pair[1]}}, "yiddish", pair[0])
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
			}
			stmt.Close()
		}
	})
}

func TestReferenceYiddishSnowballMetadata(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct { args []any; want any }{
				{[]any{"yiddish_stem", "קינדער"}, "{קינד}"}, {[]any{[]byte("yiddish_stem"), []byte("ONLY")}, "{only}"},
				{[]any{"yiddish_stem", ""}, "{}"}, {[]any{"yiddish_stem", []byte{}}, "{}"}, {[]any{"yiddish_stem", nil}, nil}, {[]any{nil, "קינדער"}, nil},
			} {
				for _, e := range []fixture.Executor{c, held} { dictionaryResult(t, p, b, e, q, tc.want, tc.args...) }
			}
			var want any = "{}"
			if b.PG { want = nil }
			for _, e := range []fixture.Executor{c, held} {
				dictionaryResult(t, p, b, e, q, want, "yiddish_stem", []byte(nil))
				rows, err := e.QueryContext(p.Ctx, q, "yiddish_stem", make(chan int))
				if rows != nil { rows.Close(); t.Fatal("argument failure exposed Rows") }
				fixture.DriverArgumentError(t, err)
			}
			for _, args := range [][]any{{"yiddish_stem"}, {"yiddish_stem", "קינדער", "extra"}} {
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if rows != nil { rows.Close(); t.Fatal("argument count failure exposed Rows") }
				fixture.DriverArgumentError(t, err)
			}
			stmt.Close()
		}
		regtsExec(t, p, b, c, `CREATE TEXT SEARCH DICTIONARY "Yiddish Quoted" (TEMPLATE=snowball,Language=yiddish)`, 0)
		for _, q := range []string{`SELECT ts_lexize('public."Yiddish Quoted"','קינדער') AS lexemes`, `SELECT ts_lexize('pg_catalog.yiddish_stem','קינדער') AS lexemes`} {
			lithuanianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{קינד}"}}))
		}
		for _, populated := range []bool{true, false} {
			for _, step := range []fixture.Step{
				yiddishDebugStep(),
				fixture.Select("SELECT to_tsvector('yiddish','קינדער RATS 123') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'123':3 'rats':2 'קינד':1"}}),
				fixture.Select("SELECT ts_lexize('yiddish_stem','קינדער') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{קינד}"}}),
			} {
				if !populated { step.Query += " WHERE false"; step.Rows = nil }
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != len(step.Types) { t.Fatal(columns, err) }
					for i, column := range columns {
						scan := reflect.TypeFor[string]()
						if b.PG && step.Types[i] != "TEXT" { scan = reflect.TypeFor[any]() }
						var length int64
						if step.Types[i] == "TEXT" { length = math.MaxInt64 }
						l, lk := column.Length()
						precision, scale, dk := column.DecimalSize()
						n, nk := column.Nullable()
						if column.Name() != step.Columns[i] || column.DatabaseTypeName() != step.Types[i] || column.ScanType() != scan || l != length || lk != (length != 0) || precision != 0 || scale != 0 || dk || n || nk { t.Fatalf("descriptor %s: %s %v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", column.Name(), column.DatabaseTypeName(), column.ScanType(), l, lk, precision, scale, dk, n, nk) }
					}
				}
				step.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					values := make([]any, len(step.Types)); dest := make([]any, len(values))
					for i := range dest { dest[i] = &values[i] }
					if err := rows.Scan(dest...); err != nil { t.Fatal(err) }
					for i, value := range values {
						if value == nil { continue }
						want := reflect.TypeFor[string]()
						if b.PG && step.Types[i] != "TEXT" { want = reflect.TypeFor[[]byte]() }
						if reflect.TypeOf(value) != want { t.Fatalf("raw %s=%T want %v", step.Columns[i], value, want) }
					}
				}
				lithuanianSmokeBoth(t, p, b, c, step)
			}
		}
		regtsSelect(t, p, b, c, "SELECT prorettype::bigint,proisstrict FROM pg_proc WHERE oid=3723", []string{"prorettype", "proisstrict"}, []string{"INT8", "BOOL"}, [][]any{{int64(1009), true}})
	})
}
