package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2Syntax(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("malformed XPath succeeded")
	}
	want := xml2Compile("Invalid expression")
	fixture.AssertDiagnostic(t, err, &want)
}

func TestReferenceXML2Diagnostics(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, doc := range []string{"<r>", "<r>&missing;</r>", "<r a='1' a='2'/>"} {
			for _, path := range []string{".", "["} {
				xml2Both(t, p, b, c, xml2Scalars(doc, path, nil, nil, false))
			}
		}
		for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
			q := "SELECT " + fn + "($1::text,$2::text)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("parameterized syntax witness must prepare", err)
			}
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ, want := "TEXT", any("12")
			switch fn {
			case "xpath_number":
				typ, want = "FLOAT4", float64(12)
			case "xpath_bool":
				typ, want = "BOOL", true
			}
			paths := []string{"[", "(", "/r[", "/r extra", "foo)"}
			if fn != "xpath_string" {
				paths = append(paths, "", " ", "\t\n")
			}
			for _, path := range paths {
				for _, prepared := range []bool{false, true} {
					var value any
					if prepared {
						err = stmt.QueryRowContext(p.Ctx, "<r/>", path).Scan(&value)
					} else {
						err = c.QueryRowContext(p.Ctx, q, "<r/>", path).Scan(&value)
					}
					xml2Syntax(t, err)
					fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{fn}, []string{typ}, [][]any{{want}}, "<r>12</r>", "."))
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, fn := range []string{"xpath_string", "xpath_number", "xpath_bool"} {
			xml2Failure(t, p, b, c, "SELECT 'é前' AS prefix,"+fn+"($1::text,$2::text) AS value", xml2Compile("Invalid expression"), false, "<r/>", "[")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_atomic(id integer,n real)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_atomic VALUES(0,99)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE xml2_source(id integer,doc text,path text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xml2_source VALUES(1,'<r>12</r>','.'),(2,'<r/>','[')", 2))
		q := "INSERT INTO xml2_atomic SELECT id,xpath_number(doc,path) FROM xml2_source ORDER BY id"
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.ExecContext(p.Ctx)
				stmt.Close()
			} else {
				_, err = c.ExecContext(p.Ctx, q)
			}
			xml2Syntax(t, err)
			xml2Both(t, p, b, c, fixture.Select("SELECT id,n FROM xml2_atomic ORDER BY id", []string{"id", "n"}, []string{"INT4", "FLOAT4"}, [][]any{{int64(0), float64(99)}}))
		}
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT xml2_recover", 0))
		_, err = tx.ExecContext(p.Ctx, q)
		xml2Syntax(t, err)
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO SAVEPOINT xml2_recover", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, xml2Scalars("<r>12</r>", ".", "12", float64(12), true))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
}
