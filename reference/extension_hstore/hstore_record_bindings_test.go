package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordHeldBindings(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, operator := range []bool{false, true} {
			q := "SELECT " + hstoreRecordCall(operator, "$1::hs_record_pair", "$2::hstore") + "::text AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ base, source, value any }{
				{"(7,keep)", "i=>0042", "(42,keep)"},
				{"(8,next)", "i=>oops", nil},
				{"(9,last)", "n=>changed", "(9,changed)"},
				{"(7,keep)", "n=>NULL", "(7,)"},
				{"(7,keep)", `n=>"NULL"`, "(7,NULL)"},
				{"(7,keep)", "", "(7,keep)"},
				{nil, "", "(,)"},
				{nil, nil, nil},
				{"(11,reuse)", nil, "(11,reuse)"},
			} {
				if tc.source == "i=>oops" {
					hstoreError(t, p, b, held, q, false, true, false, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`), tc.base, tc.source)
				} else {
					hstoreRun(t, p, b, held, hstoreRecordText(q, tc.value, tc.base, tc.source))
				}
			}
			stmt.Close()
		}
		q := "SELECT hstore($1::hs_record_pair) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			base  any
			value string
		}{
			{"(7,keep)", `"i"=>"7", "n"=>"keep"`},
			{nil, `"i"=>NULL, "n"=>NULL`},
			{"(,)", `"i"=>NULL, "n"=>NULL`},
			{"(42,next)", `"i"=>"42", "n"=>"next"`},
		} {
			hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"v"}, [][]any{{tc.value}}, nil, tc.base))
		}
	})
}

func TestReferenceHstoreRecordAtomicity(t *testing.T) {
	for _, operator := range []bool{false, true} {
		label := "function"
		if operator {
			label = "operator"
		}
		t.Run(label, func(t *testing.T) {
			hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreRecordSetup(t, p, b, c)
				hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_record_atomic(id integer,v hs_record_pair)", 0))
				hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_atomic VALUES (1,'(7,sentinel)')", 1))
				q := "INSERT INTO hs_record_atomic VALUES (2," + hstoreRecordCall(operator, "'(7,first)'::hs_record_pair", "$1::hstore") + "),(3," + hstoreRecordCall(operator, "'(8,last)'::hs_record_pair", "$2::hstore") + ")"
				for _, prepared := range []bool{false, true} {
					hstoreError(t, p, b, c, q, prepared, false, false, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`), "i=>42", "i=>oops")
					s := fixture.Select("SELECT id,v::text AS v FROM hs_record_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(7,sentinel)"}})
					hstoreRecordBoth(t, p, b, c, s)
				}
				hstoreRun(t, p, b, c, fixture.Exec(q, 2, "i=>42", "i=>43"))
				hstoreRun(t, p, b, c, fixture.Select("SELECT id,v::text AS v FROM hs_record_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(7,sentinel)"}, {int64(2), "(42,first)"}, {int64(3), "(43,last)"}}))
			})
		})
	}
}

func TestReferenceHstoreRecordCancellation(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		// These native ordinary context errors have no PostgreSQL transport analogue.
		if b.PG {
			return
		}
		for _, q := range []string{
			"SELECT hstore($1::hs_record_pair)::text AS v",
			"SELECT populate_record($1::hs_record_pair,'i=>42'::hstore)::text AS v",
			"SELECT ($1::hs_record_pair #= 'i=>42'::hstore)::text AS v",
		} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx, "(7,keep)")
				} else {
					rows, err = c.QueryContext(ctx, q, "(7,keep)")
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled query: %v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatalf("context error acquired SQLSTATE: %v", err)
				}
			}
			stmt.Close()
		}
	})
}
