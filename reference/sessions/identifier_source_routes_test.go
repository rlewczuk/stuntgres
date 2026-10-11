package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierLabelSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t (x INT)", 0), fixture.Exec("INSERT INTO t VALUES (7)", 1)} {
			r, err := db.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		for _, word := range []string{"char", "day"} {
			r, err := db.Exec("SELECT x " + word + " FROM t")
			if r != nil {
				t.Fatal("failed Exec published result")
			}
			want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \""+word+"\"")
			want.Position = "10"
			fixture.AssertDiagnostic(t, err, &want)
		}
		for _, tc := range []struct{ label, name string }{{"check", "check"}, {"authorization", "authorization"}, {"default", "default"}, {"AS char", "char"}, {"AS grant", "grant"}, {`AS "Mixed"`, "Mixed"}, {`"isnull"`, "isnull"}, {`"notnull"`, "notnull"}} {
			q := "SELECT x " + tc.label + " FROM t"
			s := fixture.Select(q, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}})
			rows, err := db.Query(q)
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceIdentifierNormalizationSourceConsumerRoutes(t *testing.T) {
	for _, tc := range identifierNormalizationCases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE "+tc.spelling+" ("+tc.spelling+" INT)", 0), fixture.Exec("INSERT INTO "+tc.spelling+" ("+tc.spelling+") VALUES (7)", 1)} {
					r, err := backend.DB.Exec(s.Query)
					fixture.DeleteReferenceCount(t, r, err, s.Affected)
				}
				q := "SELECT " + tc.spelling + " FROM " + tc.spelling + " ORDER BY " + tc.spelling
				rows, err := backend.DB.Query(q)
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select(q, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

func TestReferenceIdentifierQualifiedSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		if err := backend.DB.Ping(); err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"select", "authorization", "from", "check"} {
			q := "CREATE TABLE public." + name + " (x INT)"
			var result sql.Result
			var err error
			if i%2 == 0 {
				result, err = backend.DB.Exec(q)
			} else {
				held, e := backend.DB.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				result, err = held.Exec()
				if e := held.Close(); e != nil {
					t.Fatal(e)
				}
			}
			fixture.DeleteReferenceCount(t, result, err, 0)
			held, err := backend.DB.Prepare("INSERT INTO public." + name + " (x) VALUES ($1)")
			if err != nil {
				t.Fatal(err)
			}
			result, err = held.Exec(7 + i)
			fixture.DeleteReferenceCount(t, result, err, 1)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			q = "SELECT x FROM public." + name + " WHERE x=$1 ORDER BY x"
			s := fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}, 7+i)
			rows, err := backend.DB.Query(q, 7+i)
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			held, err = backend.DB.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query(7 + i)
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			rows, err = backend.DB.Query("SELECT x FROM \"" + name + "\" ORDER BY x")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
		}
	}
}

func TestReferenceIdentifierInsertSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t (x INT)", 0), fixture.Exec("CREATE TABLE source_rows (x INT)", 0), fixture.Exec("INSERT INTO source_rows VALUES (7)", 1)} {
			r, err := backend.DB.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		for _, q := range []string{"INSERT INTO t (SELECT)", "INSERT INTO t SELECT x FROM source_rows", "INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "INSERT INTO t (x) SELECT x FROM source_rows"} {
			r, err := backend.DB.Exec(q)
			fixture.DeleteReferenceCount(t, r, err, 1)
			held, err := backend.DB.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			r, err = held.Exec()
			fixture.DeleteReferenceCount(t, r, err, 1)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		const q = "SELECT x FROM t ORDER BY x"
		rows, err := backend.DB.Query(q)
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {nil}, {nil}}))
	}
}

func TestReferenceIdentifierCastSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(x INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1)} {
			r, err := backend.DB.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT int4(x) FROM t", []string{"int4"}, []string{"INT4"}, [][]any{{int64(7)}}),
			fixture.Select("SELECT text(x) FROM t", []string{"text"}, []string{"TEXT"}, [][]any{{"7"}}),
			fixture.Select("SELECT CAST(x AS INT) FROM t", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}),
			fixture.Select("SELECT x, CAST(x AS INT) FROM t", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}),
		} {
			rows, err := backend.DB.Query(s.Query)
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			held, err := backend.DB.Prepare(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
