package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceIdentifierLabelSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t (x INT)", 0), referenceExec("INSERT INTO t VALUES (7)", 1)} {
			r, err := db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		for _, word := range []string{"char", "day"} {
			r, err := db.Exec("SELECT x " + word + " FROM t")
			if r != nil {
				t.Fatal("failed Exec published result")
			}
			want := controlsReferenceDiagnostic("42601", "syntax error at or near \""+word+"\"")
			want.Position = "10"
			referenceAssertDiagnostic(t, err, &want)
		}
		for _, tc := range []struct{ label, name string }{{"check", "check"}, {"authorization", "authorization"}, {"default", "default"}, {"AS char", "char"}, {"AS grant", "grant"}, {`AS "Mixed"`, "Mixed"}, {`"isnull"`, "isnull"}, {`"notnull"`, "notnull"}} {
			q := "SELECT x " + tc.label + " FROM t"
			s := referenceSelect(q, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}})
			rows, err := db.Query(q)
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceIdentifierNormalizationSourceConsumerRoutes(t *testing.T) {
	for _, tc := range identifierNormalizationCases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				for _, s := range []referenceStep{referenceExec("CREATE TABLE "+tc.spelling+" ("+tc.spelling+" INT)", 0), referenceExec("INSERT INTO "+tc.spelling+" ("+tc.spelling+") VALUES (7)", 1)} {
					r, err := backend.db.Exec(s.query)
					deleteReferenceCount(t, r, err, s.affected)
				}
				q := "SELECT " + tc.spelling + " FROM " + tc.spelling + " ORDER BY " + tc.spelling
				rows, err := backend.db.Query(q)
				deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect(q, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

func TestReferenceIdentifierQualifiedSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		if err := backend.db.Ping(); err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"select", "authorization", "from", "check"} {
			q := "CREATE TABLE public." + name + " (x INT)"
			var result sql.Result
			var err error
			if i%2 == 0 {
				result, err = backend.db.Exec(q)
			} else {
				held, e := backend.db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				result, err = held.Exec()
				if e := held.Close(); e != nil {
					t.Fatal(e)
				}
			}
			deleteReferenceCount(t, result, err, 0)
			held, err := backend.db.Prepare("INSERT INTO public." + name + " (x) VALUES ($1)")
			if err != nil {
				t.Fatal(err)
			}
			result, err = held.Exec(7 + i)
			deleteReferenceCount(t, result, err, 1)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			q = "SELECT x FROM public." + name + " WHERE x=$1 ORDER BY x"
			s := referenceSelect(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(7 + i)}}, 7+i)
			rows, err := backend.db.Query(q, 7+i)
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			held, err = backend.db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query(7 + i)
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			rows, err = backend.db.Query("SELECT x FROM \"" + name + "\" ORDER BY x")
			deleteReferenceConsume(t, backend.pg, rows, err, s)
		}
	}
}

func TestReferenceIdentifierInsertSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t (x INT)", 0), referenceExec("CREATE TABLE source_rows (x INT)", 0), referenceExec("INSERT INTO source_rows VALUES (7)", 1)} {
			r, err := backend.db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		for _, q := range []string{"INSERT INTO t (SELECT)", "INSERT INTO t SELECT x FROM source_rows", "INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "INSERT INTO t (x) SELECT x FROM source_rows"} {
			r, err := backend.db.Exec(q)
			deleteReferenceCount(t, r, err, 1)
			held, err := backend.db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			r, err = held.Exec()
			deleteReferenceCount(t, r, err, 1)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		const q = "SELECT x FROM t ORDER BY x"
		rows, err := backend.db.Query(q)
		deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {nil}, {nil}}))
	}
}

func TestReferenceIdentifierCastSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t(x INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1)} {
			r, err := backend.db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		for _, s := range []referenceStep{
			referenceSelect("SELECT int4(x) FROM t", []string{"int4"}, []string{"INT4"}, [][]any{{int64(7)}}),
			referenceSelect("SELECT text(x) FROM t", []string{"text"}, []string{"TEXT"}, [][]any{{"7"}}),
			referenceSelect("SELECT CAST(x AS INT) FROM t", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}),
			referenceSelect("SELECT x, CAST(x AS INT) FROM t", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}),
		} {
			rows, err := backend.db.Query(s.query)
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			held, err := backend.db.Prepare(s.query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
