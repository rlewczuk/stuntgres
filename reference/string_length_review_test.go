package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceStringLengthStoredPresentation(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec(`CREATE TABLE sl_stored(v text,c char(4),n int DEFAULT (position('🙂' IN 'é🙂')),CONSTRAINT sl_special CHECK(position('🙂' IN v)>0),CONSTRAINT sl_ordinary CHECK("position"(v,'🙂')>0),CONSTRAINT sl_bits CHECK(bit_length(c)=48),CONSTRAINT sl_search CHECK(strpos(c,'🙂')=2),CONSTRAINT sl_octets CHECK(octet_length(c)=8))`, 0))
		for _, tc := range []struct{ name, compact, pretty string }{
			{"sl_special", `(POSITION(('🙂'::text) IN (v)) > 0)`, `POSITION(('🙂'::text) IN (v)) > 0`},
			{"sl_ordinary", `("position"(v, '🙂'::text) > 0)`, `"position"(v, '🙂'::text) > 0`},
			{"sl_bits", `(bit_length((c)::text) = 48)`, `bit_length(c::text) = 48`},
			{"sl_search", `(strpos((c)::text, '🙂'::text) = 2)`, `strpos(c::text, '🙂'::text) = 2`},
			{"sl_octets", `(octet_length(c) = 8)`, `octet_length(c) = 8`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stringLengthBoth(t, p, b, c, referenceSelect("SELECT pg_get_expr(conbin,conrelid) AS compact,pg_get_expr(conbin,conrelid,true) AS pretty FROM pg_constraint WHERE conname='"+tc.name+"'", []string{"compact", "pretty"}, []string{"TEXT", "TEXT"}, [][]any{{tc.compact, tc.pretty}}))
				stringLengthBoth(t, p, b, c, referenceSelect("SELECT pg_get_constraintdef(oid) AS compact,pg_get_constraintdef(oid,true) AS pretty FROM pg_constraint WHERE conname='"+tc.name+"'", []string{"compact", "pretty"}, []string{"TEXT", "TEXT"}, [][]any{{"CHECK ("+tc.compact+")", "CHECK ("+tc.pretty+")"}}))
			})
		}
		t.Run("position-default", func(t *testing.T) {
			stringLengthBoth(t, p, b, c, referenceSelect("SELECT pg_get_expr(a.adbin,a.adrelid) AS compact,pg_get_expr(a.adbin,a.adrelid,true) AS pretty FROM pg_attrdef a JOIN pg_class r ON a.adrelid=r.oid WHERE r.relname='sl_stored'", []string{"compact", "pretty"}, []string{"TEXT", "TEXT"}, [][]any{{`POSITION(('🙂'::text) IN ('é🙂'::text))`, `POSITION(('🙂'::text) IN ('é🙂'::text))`}}))
		})
		stringLengthBoth(t, p, b, c, referenceExec("INSERT INTO sl_stored(v,c) VALUES('é🙂','é🙂')", 1))
		rows := referenceSelect("SELECT v,c,n,octet_length(c) AS physical,bit_length(c) AS bits,strpos(c,'🙂') AS found FROM sl_stored", []string{"v", "c", "n", "physical", "bits", "found"}, []string{"TEXT", "BPCHAR", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"é🙂", "é🙂  ", int64(2), int64(8), int64(48), int64(2)}, {"é🙂", "é🙂  ", int64(2), int64(8), int64(48), int64(2)}})
		stringLengthBoth(t, p, b, c, rows)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(rows)
			// Execute the restored DEFAULT again, not just persisted row values.
			run(referenceExec("INSERT INTO sl_stored(v,c) VALUES('é🙂','é🙂')", 1))
			third := rows
			third.rows = append(append([][]any(nil), rows.rows...), []any{"é🙂", "é🙂  ", int64(2), int64(8), int64(48), int64(2)})
			run(third)
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, rows)
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO sl_stored(v,c) VALUES('é🙂','é🙂')", 1))
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, third)
				if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func TestReferenceStringLengthParameterConflicts(t *testing.T) {
	stringLengthScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			query string
			want referenceDiagnostic
		}{
			{"SELECT strpos($1,$1::varchar) AS v", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text", Position: "15"}},
			{`SELECT length($1),position($1 IN '\x41'::bytea)`, referenceDiagnostic{Code: "42883", Message: "function pg_catalog.position(bytea, text) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "19"}},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				for _, arg := range []any{"A", nil} {
					for _, prepared := range []bool{false, true} {
						t.Run(prefix+tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							want := tc.want
							want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
							if prefix != "" {
								if want.Position == "15" { want.Position = "22" } else { want.Position = "26" }
							}
							s := referenceErrorDiagnostic(prefix+tc.query, want, arg)
							s.selectRows, s.errorStage = true, "execute"
							if prepared {
								s = referencePrepared(s)
								s.errorStage = "prepare"
							}
							referenceRunStep(t, p.ctx, c, b.recorder, s)
						})
					}
				}
			}
		}
	})
}
