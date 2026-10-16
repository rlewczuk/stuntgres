package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterDomainChecks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var notices []fixture.Diagnostic
				_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = rec
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, rec, s) }
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE DOMAIN checked_pair AS pair CONSTRAINT field_check CHECK ((VALUE).id>0) CONSTRAINT keep_check CHECK ((VALUE).note<>'bad')`, `CREATE DOMAIN outer_pair AS checked_pair`, `CREATE TYPE nested AS (r outer_pair)`, `CREATE TABLE holder(k integer,d checked_pair,n nested)`)
				run(fixture.Exec(`INSERT INTO holder VALUES(1,$1,$2),(2,NULL,NULL)`, 2, "(7,x)", `("(9,y)")`))
				var identities [3]int64
				if err := c.QueryRowContext(p.Ctx, `SELECT 'pair'::regtype::oid,'checked_pair'::regtype::oid,'outer_pair'::regtype::oid`).Scan(&identities[0], &identities[1], &identities[2]); err != nil {
					t.Fatal(err)
				}
				constraints := func(names [][]any) {
					run(fixture.Select(`SELECT conname FROM pg_constraint WHERE contypid='checked_pair'::regtype::oid ORDER BY conname`, []string{"conname"}, []string{"NAME"}, names))
				}
				constraints([][]any{{"field_check"}, {"keep_check"}})
				for _, input := range []string{"(0,ok)", "(7,bad)"} {
					name := "field_check"
					if input == "(7,bad)" {
						name = "keep_check"
					}
					want := fixture.CompositeReferenceDiagnostic("23514", `value for domain checked_pair violates check constraint "`+name+`"`)
					want.Schema, want.DataType, want.Constraint = "public", "checked_pair", name
					want.Context = `unnamed portal parameter $1 = '...'`
					fixture.CompositeReferenceError(t, p, c, `INSERT INTO holder(k,d) VALUES(4,$1)`, "execute", want, input)
				}
				d := fixture.CompositeReferenceDiagnostic("2BP01", `cannot drop column id of composite type pair because other objects depend on it`)
				d.Detail = `constraint field_check depends on column id of composite type pair`
				d.Hint = `Use DROP ... CASCADE to drop the dependent objects too.`
				notices = nil
				fixture.CompositeReferenceError(t, p, c, `ALTER TYPE pair DROP ATTRIBUTE id RESTRICT`, "execute", d)
				if len(notices) != 0 {
					t.Errorf("RESTRICT notices=%#v want none", notices)
				}
				constraints([][]any{{"field_check"}, {"keep_check"}})
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT d AS v FROM holder ORDER BY k`, [][]any{{"(7,x)"}, {nil}}, prepared)
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT n AS v FROM holder ORDER BY k`, [][]any{{`("(9,y)")`}, {nil}}, prepared)
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Select(`SELECT attname FROM pg_attribute WHERE attrelid='pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"note"}}))
				notices = nil
				run(fixture.Exec(`ALTER TYPE pair DROP ATTRIBUTE id CASCADE`, 0))
				fixture.DomainReferenceNotice(t, &notices, `drop cascades to constraint field_check`, "")
				constraints([][]any{{"keep_check"}})
				var after [3]int64
				if err := c.QueryRowContext(p.Ctx, `SELECT 'pair'::regtype::oid,'checked_pair'::regtype::oid,'outer_pair'::regtype::oid`).Scan(&after[0], &after[1], &after[2]); err != nil || after != identities {
					t.Fatalf("domain/source identity changed: before=%v after=%v err=%v", identities, after, err)
				}
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT d AS v FROM holder ORDER BY k`, [][]any{{"(x)"}, {nil}}, prepared)
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT n AS v FROM holder ORDER BY k`, [][]any{{`("(y)")`}, {nil}}, prepared)
				run(fixture.Exec(`INSERT INTO holder VALUES(3,$1,$2)`, 1, "(free)", `("(nested)")`))
				// Surviving CHECK still rejects future scalar and nested conversions.
				for _, q := range []string{`INSERT INTO holder(k,d) VALUES(4,$1)`, `INSERT INTO holder(k,n) VALUES(4,$1)`} {
					input := "(bad)"
					if q == `INSERT INTO holder(k,n) VALUES(4,$1)` {
						input = `("(bad)")`
					}
					domainName := "checked_pair"
					if q == `INSERT INTO holder(k,n) VALUES(4,$1)` {
						domainName = "outer_pair"
					}
					want := fixture.CompositeReferenceDiagnostic("23514", `value for domain `+domainName+` violates check constraint "keep_check"`)
					want.Schema, want.DataType, want.Constraint = "public", domainName, "keep_check"
					want.Context = `unnamed portal parameter $1 = '...'`
					fixture.CompositeReferenceError(t, p, c, q, "execute", want, input)
				}
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT d AS v FROM holder ORDER BY k`, [][]any{{"(x)"}, {nil}, {"(free)"}}, prepared)
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT n AS v FROM holder ORDER BY k`, [][]any{{`("(y)")`}, {nil}, {`("(nested)")`}}, prepared)
				constraints([][]any{{"keep_check"}})
			})
		})
	}
}
