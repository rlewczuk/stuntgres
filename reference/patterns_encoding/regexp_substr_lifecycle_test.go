package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRegexpSubstrHeldParameters(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const q = `SELECT regexp_substr($1,$2,$3,$4,$5,$6) AS result`
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		var typedNil *string
		for _, c := range []struct {
			args      []any
			want      any
			code, msg string
		}{
			{[]any{"foobar", "o(.)b", 1, 1, "", 0}, "oob", "", ""},
			{[]any{"ABCDEF", "c(.)(..)", 1, 1, "i", 2}, "EF", "", ""},
			{[]any{"a1b22", "[0-9]+", 2, 2, "", 0}, "22", "", ""},
			{[]any{typedNil, "[", 0, 0, "z", -1}, nil, "", ""},
			{[]any{"a", "a", 1, 0, "", 0}, nil, "22023", `invalid value for parameter "n": 0`},
			{[]any{"a", "a", 1, 1, "z", 0}, nil, "22023", `invalid regular expression option: "z"`},
			{[]any{"a", "[", 1, 1, "", 0}, nil, "2201B", `invalid regular expression: brackets [] not balanced`},
			{[]any{"é🙂x", "(.)", 2, 1, "", 1}, "🙂", "", ""},
			{[]any{"a", "a", 1, 1, typedNil, 0}, nil, "", ""},
			{[]any{"abc", "b", 1, 1, "", 1}, "b", "", ""},
		} {
			s := regexpRows(b, q, []string{"result"}, []string{"TEXT"}, [][]any{{c.want}}, c.args...)
			if c.code != "" {
				s = regexpRuntimeError(q, c.code, c.msg, "", c.args...)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		input, pattern := []byte("aba"), []byte("(a)")
		rows, err := stmt.QueryContext(p.Ctx, input, pattern, 1, 1, "", 1)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		input[0] = 'z'
		pattern[1] = 'z'
		fixture.StringCoreRetainedRows(t, rows, nil, []string{"result"}, []string{"TEXT"}, [][]any{{"a"}}, b.PG)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, q, []string{"result"}, []string{"TEXT"}, [][]any{{"z"}}, input, pattern, 1, 1, "", 1))
		var retained string
		if err := stmt.QueryRowContext(p.Ctx, "foobar", "o(.)b", 1, 1, "", 0).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, q, []string{"result"}, []string{"TEXT"}, [][]any{{"x"}}, "x", "x", 1, 1, "", 0))
		if retained != "oob" {
			t.Fatal("retained value changed", retained)
		}
	})
}

func TestReferenceRegexpSubstrAtomicity(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE substr_atomic(id INT,s TEXT,p TEXT,sentinel INT)`, 0))
		run(fixture.Exec(`INSERT INTO substr_atomic VALUES(1,'a','a',7),(2,'a','[',7)`, 2))
		run(fixture.Exec(`CREATE TABLE substr_generated(s TEXT,p TEXT,g TEXT GENERATED ALWAYS AS (regexp_substr(s,p)) STORED)`, 0))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{`UPDATE substr_atomic SET s=regexp_substr(s,p),sentinel=99`, `INSERT INTO substr_generated(s,p) VALUES('a','a'),('a','[')`} {
				s := similarReferenceRegexError(q, "brackets [] not balanced", "")
				s.SelectRows = false
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT s,sentinel FROM substr_atomic ORDER BY id`, []string{"s", "sentinel"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(7)}, {"a", int64(7)}}))
				run(fixture.Select(`SELECT g FROM substr_generated`, []string{"g"}, []string{"TEXT"}, nil))
			}
		}
		run(fixture.Exec(`UPDATE substr_atomic SET sentinel=8 WHERE id=1 AND regexp_substr('a','a')='a'`, 1))
		run(fixture.Select(`SELECT sentinel FROM substr_atomic ORDER BY id`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(7)}}))
	})
}

const substrStoredDDL = `CREATE TABLE substr_stored(id INT,s TEXT,d TEXT DEFAULT regexp_substr('a1b22','[0-9]+',1,2),g TEXT GENERATED ALWAYS AS (regexp_substr(s,'([A-Z])([0-9]+)',1,1,'',2)) STORED,CHECK(regexp_substr(subexpr=>2,flags=>'',"N"=>1,start=>1,pattern=>'([A-Z])([0-9]+)',string=>s) IS NOT NULL))`
const substrStoredQuery = `SELECT d,g FROM substr_stored ORDER BY id`

func substrStoredWant() fixture.Step {
	return fixture.Select(substrStoredQuery, []string{"d", "g"}, []string{"TEXT", "TEXT"}, [][]any{{"22", "12"}})
}

func TestReferenceRegexpSubstrStoredControls(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(substrStoredDDL, 0))
		run(fixture.Exec(`CREATE INDEX substr_idx ON substr_stored((regexp_substr("N"=>1,start=>1,pattern=>'[0-9]+',string=>s)))`, 0))
		run(fixture.Exec(`CREATE VIEW substr_view AS SELECT id,regexp_substr(subexpr=>2,flags=>'',"N"=>1,start=>1,pattern=>'([A-Z])([0-9]+)',string=>s) AS piece FROM substr_stored`, 0))
		run(fixture.Exec(`INSERT INTO substr_stored(id,s) VALUES(1,'A12')`, 1))
		regexpRoutes(t, p, b, substrStoredWant())
		for _, c := range []struct {
			n    int
			expr string
		}{
			{3, `regexp_substr('a1b22'::text, '[0-9]+'::text, 1, 2)`},
			{4, `regexp_substr(s, '([A-Z])([0-9]+)'::text, 1, 1, ''::text, 2)`},
		} {
			regexpRoutes(t, p, b, fixture.Select(`SELECT pg_get_expr(d.adbin,d.adrelid) AS expression FROM pg_attrdef d WHERE d.adrelid='substr_stored'::regclass::oid AND d.adnum=$1`, []string{"expression"}, []string{"TEXT"}, [][]any{{c.expr}}, c.n))
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, substrStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: substrStoredQuery}
		run(fixture.Exec(`ALTER TABLE substr_stored RENAME COLUMN s TO source`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, substrStoredWant())
		view := fixture.Select(`SELECT piece FROM substr_view ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"12"}})
		regexpRoutes(t, p, b, view)
		if b.PG {
			return
		} // Native replacement controls have no PG API counterpart.
		old, err := p.Native.QueryContext(p.Ctx, substrStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.StringCoreRetainedRows(t, old, nil, []string{"d", "g"}, []string{"TEXT", "TEXT"}, [][]any{{"22", "12"}}, false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, substrStoredWant())
		run(view)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unstable redump", err)
		}
		bad := dump.String() + `CREATE TABLE substr_late(a TEXT DEFAULT regexp_substr('a','[')); INSERT INTO substr_late(a) VALUES('ok'); INSERT INTO substr_late DEFAULT VALUES;`
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
		d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201B", Message: "invalid regular expression: brackets [] not balanced"}
		fixture.AssertDiagnostic(t, err, &d)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(after.Bytes(), redump.Bytes()) {
			t.Fatal("failed restore published", err)
		}
		run(fixture.Exec(`INSERT INTO substr_stored(id,source) VALUES(2,'B34')`, 1))
		want := substrStoredWant()
		want.Rows = append(want.Rows, []any{"22", "34"})
		run(want)
		view.Rows = append(view.Rows, []any{"34"})
		run(view)
		if p.Postgres != nil {
			for _, q := range []string{`DROP VIEW IF EXISTS substr_view; DROP TABLE IF EXISTS substr_stored CASCADE`, dump.String()} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, substrStoredWant())
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO substr_stored(id,source) VALUES(2,'B34')`, 1))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, view)
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP VIEW substr_view; DROP TABLE substr_stored`); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceRegexpSubstrCancellation(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE substr_cancel(s TEXT,sentinel INT)`, 0))
		source := strings.Repeat("a", 2*1024*1024)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO substr_cancel VALUES($1,7)`, 1, source))
		conn, blocker := fixture.LockConnections(t, p.Ctx, b.DB)
		identity := fixture.SettingsPhysical(t, conn)
		check := func() {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(`SELECT sentinel FROM substr_cancel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if fixture.SettingsPhysical(t, conn) != identity {
				t.Fatal("regexp_substr recovery lost physical owner")
			}
		}
		const q = `UPDATE substr_cancel SET sentinel=99 WHERE regexp_substr(s,'',1,2147483647) IS NOT NULL`
		for _, prepared := range []bool{false, true} {
			var stmt *sql.Stmt
			var err error
			if prepared {
				stmt, err = conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
			}
			exec := func(ctx context.Context) (sql.Result, error) {
				if stmt != nil {
					return stmt.ExecContext(ctx)
				}
				return conn.ExecContext(ctx, q)
			}
			fixture.RunStep(t, p.Ctx, blocker, b.Recorder, fixture.Exec(`BEGIN`, 0))
			released := false
			t.Cleanup(func() {
				if !released {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, _ = blocker.ExecContext(ctx, `ROLLBACK`)
				}
			})
			fixture.RunStep(t, p.Ctx, blocker, b.Recorder, fixture.Exec(`LOCK TABLE substr_cancel IN ACCESS EXCLUSIVE MODE`, 0))
			// Durable admission is a relation wait, not regexp provider entry.
			fixture.RelationLockActiveCancel(t, p, b, conn, q, func(ctx context.Context) fixture.RelationOutcome {
				if !b.PG {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
					defer cancel()
				}
				result, err := exec(ctx)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				fixture.RunStep(t, ctx, blocker, b.Recorder, fixture.Exec(`ROLLBACK`, 0))
			}()
			released = true
			check()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, regexpOne(b, `regexp_substr('abc','b')`, "TEXT", "b"))
			if _, err := conn.ExecContext(p.Ctx, `BEGIN`); err != nil {
				t.Fatal(err)
			}
			func() {
				rolledBack := false
				defer func() {
					if !rolledBack {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						_, _ = conn.ExecContext(ctx, `ROLLBACK`)
					}
				}()
				if _, err := conn.ExecContext(p.Ctx, `SET LOCAL statement_timeout='10ms'`); err != nil {
					t.Fatal(err)
				}
				s := regexpRuntimeError(q, "57014", "canceling statement due to statement timeout", "")
				s.SelectRows = false
				if prepared {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
				} else {
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Exec(`ROLLBACK`, 0))
				rolledBack = true
			}()
			check()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`UPDATE substr_cancel SET s='a'`, 1))
			result, err := exec(p.Ctx)
			if err != nil || result == nil {
				t.Fatal("same-consumer recovery", result, err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 0 {
				t.Fatal("same-consumer recovery affected count", n, err)
			}
			check()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`UPDATE substr_cancel SET s=$1`, 1, source))
		}
	})
}
