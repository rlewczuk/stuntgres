package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// The declaration spelling is DOCUMENT-only: CONTENT input rejects it, and
// typed XML output warns on it, so leaves are read through ::text only.
const (
	arrayXMLDecl   = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`
	arrayXMLParsed = `XMLPARSE(DOCUMENT '` + arrayXMLDecl + `')`
)

// arrayXMLSource stores mixed DOCUMENT/CONTENT leaves with explicit modes, never
// one session xmloption, at one-based, shifted, multidimensional and rank-6 bounds.
func arrayXMLSource(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE public.array_xml (id INT, a XML[])", 0))
	for _, q := range []string{
		"INSERT INTO public.array_xml VALUES (1, ARRAY[" + arrayXMLParsed + ", XMLPARSE(CONTENT '<b/><c/>'), NULL, XMLPARSE(CONTENT '')])",
		"INSERT INTO public.array_xml (id, a[0:2]) VALUES (2, ARRAY[XMLPARSE(CONTENT 'x<y/>'), " + arrayXMLParsed + ", NULL])",
		"INSERT INTO public.array_xml (id, a[5:6][8:9]) VALUES (3, ARRAY[" + arrayXMLParsed + ", XMLPARSE(CONTENT '<b/><c/>'), XMLPARSE(CONTENT ''), NULL::xml])",
		"INSERT INTO public.array_xml (id, a[-10:-10][0:0][4:4][-3:-3][8:9][2:3]) VALUES (5, ARRAY[" + arrayXMLParsed + ", NULL::xml, XMLPARSE(CONTENT ''), XMLPARSE(CONTENT 'x<y/>')])",
	} {
		run(fixture.Exec(q, 1))
	}
	run(fixture.Prepared(fixture.Exec("INSERT INTO public.array_xml (id, a[5:6][8:9]) VALUES (4, ARRAY[XMLPARSE(DOCUMENT $1), XMLPARSE(CONTENT $2), XMLPARSE(CONTENT $3), NULL::xml])", 1, arrayXMLDecl, "<b/><c/>", "")))
	run(fixture.Exec("INSERT INTO public.array_xml VALUES (6, NULL), (7, '{}')", 2))
}

// arrayXMLLeaves reads every leaf in row-major order with its exact characters.
func arrayXMLLeaves() fixture.Step {
	witness := func(id int64) [][]any {
		return [][]any{{id, "[5:6][8:9]", int64(4), int64(1), arrayXMLDecl, false}, {id, "[5:6][8:9]", int64(4), int64(2), "<b/><c/>", false}, {id, "[5:6][8:9]", int64(4), int64(3), "", false}, {id, "[5:6][8:9]", int64(4), int64(4), nil, true}}
	}
	rank6 := "[-10:-10][0:0][4:4][-3:-3][8:9][2:3]"
	rows := [][]any{
		{int64(1), "[1:4]", int64(4), int64(1), arrayXMLDecl, false}, {int64(1), "[1:4]", int64(4), int64(2), "<b/><c/>", false}, {int64(1), "[1:4]", int64(4), int64(3), nil, true}, {int64(1), "[1:4]", int64(4), int64(4), "", false},
		{int64(2), "[0:2]", int64(3), int64(1), "x<y/>", false}, {int64(2), "[0:2]", int64(3), int64(2), arrayXMLDecl, false}, {int64(2), "[0:2]", int64(3), int64(3), nil, true},
	}
	rows = append(append(rows, witness(3)...), witness(4)...)
	rows = append(rows, [][]any{
		{int64(5), rank6, int64(4), int64(1), arrayXMLDecl, false}, {int64(5), rank6, int64(4), int64(2), nil, true}, {int64(5), rank6, int64(4), int64(3), "", false}, {int64(5), rank6, int64(4), int64(4), "x<y/>", false},
		{int64(6), nil, nil, nil, nil, true}, {int64(7), nil, int64(0), nil, nil, true},
	}...)
	return fixture.Select("SELECT x.id, array_dims(x.a) AS dims, cardinality(x.a) AS n, u.ord, u.e::text AS leaf, u.e IS NULL AS hole FROM public.array_xml x LEFT JOIN LATERAL unnest(x.a) WITH ORDINALITY AS u(e, ord) ON TRUE ORDER BY x.id, u.ord", []string{"id", "dims", "n", "ord", "leaf", "hole"}, []string{"INT4", "TEXT", "INT4", "INT8", "TEXT", "BOOL"}, rows)
}

// arrayXMLPositions addresses leaves by their actual subscripts.
func arrayXMLPositions() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT array_lower(a, 1) AS lo, array_upper(a, 1) AS hi, a[0]::text AS p0, a[1]::text AS p1, a[2] IS NULL AS p2, a[3] IS NULL AS p3 FROM public.array_xml WHERE id = 2", []string{"lo", "hi", "p0", "p1", "p2", "p3"}, []string{"INT4", "INT4", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(0), int64(2), "x<y/>", arrayXMLDecl, true, true}}),
		fixture.Select("SELECT id, array_dims(a) AS dims, a[5][8]::text AS a58, a[5][9]::text AS a59, a[6][8]::text AS a68, a[6][8] IS NULL AS null68, a[6][9] IS NULL AS null69 FROM public.array_xml WHERE id IN (3, 4) ORDER BY id", []string{"id", "dims", "a58", "a59", "a68", "null68", "null69"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(3), "[5:6][8:9]", arrayXMLDecl, "<b/><c/>", "", false, true}, {int64(4), "[5:6][8:9]", arrayXMLDecl, "<b/><c/>", "", false, true}}),
		fixture.Select("SELECT array_ndims(a) AS rank, a[-10][0][4][-3][8][2]::text AS first, a[-10][0][4][-3][8][3] IS NULL AS hole, a[-10][0][4][-3][9][2]::text AS empty, a[-10][0][4][-3][9][3]::text AS last, a[1][1][1][1][1][1] IS NULL AS outside FROM public.array_xml WHERE id = 5", []string{"rank", "first", "hole", "empty", "last", "outside"}, []string{"INT4", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(6), arrayXMLDecl, true, "", "x<y/>", true}}),
		fixture.Select("SELECT id, a IS NULL AS absent, cardinality(a) AS n, array_ndims(a) AS rank, pg_typeof(a)::text AS t FROM public.array_xml WHERE id IN (6, 7) ORDER BY id", []string{"id", "absent", "n", "rank", "t"}, []string{"INT4", "BOOL", "INT4", "INT4", "TEXT"}, [][]any{{int64(6), true, nil, nil, "xml[]"}, {int64(7), false, int64(0), nil, "xml[]"}}),
	}
}

// arrayXMLReplayPostgres executes the actual native dump on the oracle under
// both session modes; explicit leaf modes must make the result independent.
func arrayXMLReplayPostgres(t *testing.T, p *fixture.Pair, dump string, checks ...fixture.Step) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	conn, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, mode := range []string{"DOCUMENT", "CONTENT"} {
		for _, q := range []string{"DROP SCHEMA public CASCADE; CREATE SCHEMA public", "SET XML OPTION " + mode, dump} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Fatal("PostgreSQL replay of native dump under", mode, err)
			}
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
		}
	}
	if _, err := conn.ExecContext(p.Ctx, "RESET xmloption; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
}
