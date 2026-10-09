package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestReferenceDomainArrayEscapedNull(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		run := func(s referenceStep) {
			t.Helper()
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		run(referenceExec("CREATE DOMAIN domain_s.escaped_names AS name[]", 0))
		run(referenceExec("CREATE TABLE domain_s.escaped_values(id integer,v domain_s.escaped_names)", 0))
		cases := []struct {
			name          string
			input, output any
		}{
			{"ordinary element NULL", `{NULL}`, `{NULL}`},
			{"quoted element NULL", `{"NULL"}`, `{"NULL"}`},
			{"leading escape", `{\NULL}`, `{"NULL"}`},
			{"middle escape", `{N\ULL}`, `{"NULL"}`},
			{"trailing escape", `{NUL\L}`, `{"NULL"}`},
			{"all characters escaped", `{\N\U\L\L}`, `{"NULL"}`},
			{"lowercase escaped spelling", `{\null}`, `{"null"}`},
			{"mixed distinctions", `{\NULL,NULL,"NULL",N\ULL,NUL\L}`, `{"NULL",NULL,"NULL","NULL","NULL"}`},
			{"whole SQL NULL", nil, nil},
		}
		want := make([][]any, 0, len(cases)*4)
		id := int64(0)
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SELECT $1::domain_s.escaped_names AS v FROM public.one", "SELECT CAST($1 AS domain_s.escaped_names) AS v FROM public.one"} {
						s := referenceSelect(q, []string{"v"}, []string{"_NAME"}, [][]any{{tc.output}}, tc.input)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, conn, recorder, s)
					}
					for _, value := range []string{"$2", "CAST($2 AS domain_s.escaped_names)"} {
						id++
						s := referenceExec("INSERT INTO domain_s.escaped_values VALUES($1,"+value+")", 1, id, tc.input)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, conn, recorder, s)
						want = append(want, []any{id, tc.output})
					}
				}
			})
		}
		stored := referenceSelect("SELECT id,v FROM domain_s.escaped_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "_NAME"}, want)
		run(stored)
		run(referencePrepared(stored))
		if backend.pg {
			return // PostgreSQL has no native logical Dump/Reset/Restore API.
		}
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
		run(stored)
		run(referencePrepared(stored))
	})
}
