package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceArrayTypmods(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.array_typmods (id INT,v VARCHAR(3)[],c CHAR(3)[],n NUMERIC(5,2)[],t TIME(3)[],s TIMESTAMP(3)[])", 0))
		values := []any{"[0:1]={abc,NULL}", "{a,NULL}", "{12.345,NULL}", "{12:34:56.123789,NULL}", `{"2024-01-02 12:34:56.123789",NULL}`}
		run(referenceExec(`INSERT INTO public.array_typmods VALUES (1,'[0:1]={abc,NULL}','{a,NULL}','{12.345,NULL}','{12:34:56.123789,NULL}','{"2024-01-02 12:34:56.123789",NULL}')`, 1))
		run(referencePrepared(referenceExec("INSERT INTO public.array_typmods VALUES (2,$1,$2,$3,$4,$5)", 1, values...)))
		types := []string{"INT4", "_VARCHAR", "_BPCHAR", "_NUMERIC", "_TIME", "_TIMESTAMP"}
		expected := [][]any{{int64(1), "[0:1]={abc,NULL}", `{"a  ",NULL}`, "{12.35,NULL}", "{12:34:56.124,NULL}", `{"2024-01-02 12:34:56.124",NULL}`}, {int64(2), "[0:1]={abc,NULL}", `{"a  ",NULL}`, "{12.35,NULL}", "{12:34:56.124,NULL}", `{"2024-01-02 12:34:56.124",NULL}`}}
		run(arrayReferenceSelect("SELECT id,v,c,n,t,s FROM public.array_typmods ORDER BY id", []string{"id", "v", "c", "n", "t", "s"}, types, types, expected, b.pg))
		run(arrayReferenceSelect("SELECT '{abcd}'::VARCHAR(3)[] AS a FROM public.one", []string{"a"}, []string{"_VARCHAR"}, []string{"_VARCHAR"}, [][]any{{"{abc}"}}, b.pg))
		for _, prepared := range []bool{false, true} {
			want := controlsReferenceDiagnostic("22001", "value too long for type character varying(3)")
			s := referenceErrorDiagnostic("INSERT INTO public.array_typmods (id,v) VALUES (3,'{abcd}')", want)
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			s = referenceErrorDiagnostic("INSERT INTO public.array_typmods (id,v) VALUES (4,$1)", want, "{abcd}")
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			s = referenceErrorDiagnostic("INSERT INTO public.array_typmods (id,v) VALUES (6,$1),(7,$2)", want, "{abc}", "{abcd}")
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			overflow := controlsReferenceDiagnostic("22003", "numeric field overflow")
			overflow.Detail = "A field with precision 5, scale 2 must round to an absolute value less than 10^3."
			s = referenceErrorDiagnostic("INSERT INTO public.array_typmods (id,n) VALUES (5,$1)", overflow, "{999.995}")
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
		}
		run(referenceSelect("SELECT id FROM public.array_typmods ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}

func TestReferenceArrayIntervalPhases(t *testing.T) {
	cases := []struct{ label, declaration, input, array, element, domainLiteral, domainParameter string }{
		{"day bare", "INTERVAL DAY", "{1}", "{00:00:00}", `{"1 day"}`, "{00:00:00}", `{"1 day"}`},
		{"hour bare", "INTERVAL HOUR", "{1}", "{00:00:00}", "{01:00:00}", "{00:00:00}", "{01:00:00}"},
		{"hour explicit", "INTERVAL HOUR", `{"1 hour"}`, "{01:00:00}", "{01:00:00}", "{01:00:00}", "{01:00:00}"},
		{"precision", "INTERVAL DAY TO SECOND(3)", `{"1 day 02:03:04.123789",NULL}`, `{"1 day 02:03:04.124",NULL}`, `{"1 day 02:03:04.124",NULL}`, `{"1 day 02:03:04.124",NULL}`, `{"1 day 02:03:04.124",NULL}`},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec("CREATE DOMAIN public.array_interval_element AS "+tc.declaration, 0))
				run(referenceExec("CREATE DOMAIN public.array_interval_whole AS "+tc.declaration+"[]", 0))
				run(referenceExec("CREATE TABLE public.array_interval (a "+tc.declaration+"[])", 0))
				elementName := "_ARRAY_INTERVAL_ELEMENT"
				if b.pg {
					elementName = ""
				}
				for _, usage := range []struct{ typ, name, literal, parameter string }{
					{tc.declaration + "[]", "_INTERVAL", tc.array, tc.array},
					{"public.array_interval_element[]", elementName, tc.element, tc.element},
					{"public.array_interval_whole", "_INTERVAL", tc.domainLiteral, tc.domainParameter},
				} {
					for _, prepared := range []bool{false, true} {
						literal := arrayReferenceSelect("SELECT "+arrayReferenceQuote(tc.input)+"::"+usage.typ+" AS a FROM public.one", []string{"a"}, []string{usage.name}, []string{"_INTERVAL"}, [][]any{{usage.literal}}, b.pg)
						literal.prepared = prepared
						run(literal)
						parameter := arrayReferenceSelect("SELECT $1::"+usage.typ+" AS a FROM public.one", []string{"a"}, []string{usage.name}, []string{"_INTERVAL"}, [][]any{{usage.parameter}}, b.pg, tc.input)
						parameter.prepared = prepared
						run(parameter)
					}
				}
				for _, prepared := range []bool{false, true} {
					literal := referenceExec("INSERT INTO public.array_interval VALUES ("+arrayReferenceQuote(tc.input)+")", 1)
					literal.prepared = prepared
					run(literal)
					stored := [][]any{{tc.array}}
					run(arrayReferenceSelect("SELECT a FROM public.array_interval", []string{"a"}, []string{"_INTERVAL"}, []string{"_INTERVAL"}, stored, b.pg))
					run(referenceExec("DELETE FROM public.array_interval", 1))
					parameter := referenceExec("INSERT INTO public.array_interval VALUES ($1)", 1, tc.input)
					parameter.prepared = prepared
					run(parameter)
					run(arrayReferenceSelect("SELECT a FROM public.array_interval", []string{"a"}, []string{"_INTERVAL"}, []string{"_INTERVAL"}, stored, b.pg))
					run(referenceExec("DELETE FROM public.array_interval", 1))
				}
			})
		})
	}
}

func TestReferenceArrayMetadata(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE DOMAIN public.array_md AS INT", 0))
		run(referenceExec("CREATE DOMAIN public.array_ma AS INT[]", 0))
		run(referenceExec("CREATE TABLE public.array_metadata (id INT,a INT[] NOT NULL,v VARCHAR(3)[],n NUMERIC(5,2)[],u NUMERIC[],d public.array_md[],w public.array_ma)", 0))
		run(referenceExec("INSERT INTO public.array_metadata VALUES (1,'{7}','{abc}','{12.35}','{12.345}','{8}','{9}'),(2,'{NULL}',NULL,NULL,NULL,NULL,NULL)", 2))
		types := []string{"_INT4", "_VARCHAR", "_NUMERIC", "_NUMERIC", "_ARRAY_MD", "_INT4"}
		if b.pg {
			types[4] = ""
		}
		raw := []string{"_INT4", "_VARCHAR", "_NUMERIC", "_NUMERIC", "_INT4", "_INT4"}
		for _, tc := range []struct {
			clause string
			rows   [][]any
		}{
			{"id=1", [][]any{{"{7}", "{abc}", "{12.35}", "{12.345}", "{8}", "{9}"}}},
			{"id=2", [][]any{{"{NULL}", nil, nil, nil, nil, nil}}}, {"id=99", [][]any{}},
		} {
			for _, prepared := range []bool{false, true} {
				s := arrayReferenceSelect("SELECT a,v,n,u,d,w FROM public.array_metadata WHERE "+tc.clause, []string{"a", "v", "n", "u", "d", "w"}, types, raw, tc.rows, b.pg)
				basic := s.checkMetadata
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
					basic(t, rows)
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, column := range columns {
						nullable, known := column.Nullable()
						wantNullable, wantKnown := i != 0, true
						if b.pg {
							wantNullable, wantKnown = false, false
						}
						if nullable != wantNullable || known != wantKnown {
							t.Fatalf("column %d Nullable=%t/%t want %t/%t", i, nullable, known, wantNullable, wantKnown)
						}
						precision, scale, known := column.DecimalSize()
						if i == 2 {
							if precision != 5 || scale != 2 || !known {
								t.Fatalf("sized numeric array decimal=%d,%d/%t", precision, scale, known)
							}
						} else if i != 3 && (precision != 0 || scale != 0 || known) {
							t.Fatalf("non-numeric array decimal=%d,%d/%t", precision, scale, known)
						}
						if i == 3 && !b.pg && (precision != 0 || scale != 0 || known) {
							t.Fatalf("unsized native numeric array decimal=%d,%d/%t", precision, scale, known)
						}
						if i == 3 && b.pg && (precision != 65535 || scale != 65531 || !known) {
							t.Fatalf("unsized pq numeric array decimal=%d,%d/%t", precision, scale, known)
						}
					}
				}
				s.prepared = prepared
				run(s)
			}
		}
	})
}
