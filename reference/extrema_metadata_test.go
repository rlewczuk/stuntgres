package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceExtremaMetadata(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_metadata(s VARCHAR(4) COLLATE \"C\",c CHAR(4),n NUMERIC(10,4),tm TIME(3),i INTEGER NOT NULL)", 0))
		run(referenceExec("INSERT INTO extrema_metadata VALUES('z ','a',1.2300,'12:34:56.123',4)", 1))
		types := []string{"TEXT", "BPCHAR", "NUMERIC", "TIME", "INT4"}
		row := []any{"z ", "a   ", extremaRawDatum(t, pg, "NUMERIC", "1.2300"), extremaRawDatum(t, pg, "TIME", "12:34:56.123"), int64(4)}
		query := "SELECT min(s),max(c),min(n),max(tm),min(i) FROM extrema_metadata"
		for _, suffix := range []string{"", " WHERE FALSE"} {
			want := [][]any{row}
			if suffix != "" {
				want = [][]any{{nil, nil, nil, nil, nil}}
			}
			t.Run(query+suffix, func(t *testing.T) {
				extremaResult(t, p, conn, recorder, pg, query+suffix, []string{"min", "max", "min", "max", "min"}, types, want)
			})
		}
		run(referenceExec("CREATE VIEW extrema_metadata_view AS SELECT min(s) AS s,max(c) AS c,min(n) AS n,max(tm) AS tm,min(i) AS i FROM extrema_metadata", 0))
		extremaResult(t, p, conn, recorder, pg, "SELECT * FROM extrema_metadata_view", []string{"s", "c", "n", "tm", "i"}, types, [][]any{row})
		if pg {
			// pg_attribute OIDs expose the selected overload, unrestricted typmod
			// and source C/default/noncollatable collation independently of pq.
			query := "SELECT attname,atttypid::text,atttypmod,attcollation::text FROM pg_catalog.pg_attribute WHERE attrelid='extrema_metadata_view'::regclass AND attnum>0 ORDER BY attnum"
			extremaResult(t, p, conn, recorder, pg, query, []string{"attname", "atttypid", "atttypmod", "attcollation"}, []string{"NAME", "TEXT", "INT4", "TEXT"}, [][]any{
				{[]byte("s"), "25", int64(-1), "950"},
				{[]byte("c"), "1042", int64(-1), "100"},
				{[]byte("n"), "1700", int64(-1), "0"},
				{[]byte("tm"), "1083", int64(-1), "0"},
				{[]byte("i"), "23", int64(-1), "0"},
			})
		}
		extremaResult(t, p, conn, recorder, pg, "SELECT min('0'::regclass),max('4294967295'::regclass) FROM extrema_metadata", []string{"min", "max"}, []string{"OID", "OID"}, [][]any{{extremaRawDatum(t, pg, "OID", "0"), extremaRawDatum(t, pg, "OID", "4294967295")}})
	})
}

func TestReferenceExtremaTransitions(t *testing.T) {
	// Tiny insertion sequences witness the release transition contract, not a
	// general ordering promise for SQL aggregates over unordered relations.
	for _, tc := range []struct{ name, ddl, typ, values, retained, text string }{
		{"numeric_forward", "NUMERIC", "NUMERIC", "(1.0),(1.00)", "1.00", "1.00"},
		{"numeric_reverse", "NUMERIC", "NUMERIC", "(1.00),(1.0)", "1.0", "1.0"},
		{"bpchar_forward", "BPCHAR", "BPCHAR", "('a'),('a ')", "a", "a"},
		{"bpchar_reverse", "BPCHAR", "BPCHAR", "('a '),('a')", "a ", "a"},
		{"interval_forward", "INTERVAL", "INTERVAL", "('1 mon'),('30 days')", "30 days", "30 days"},
		{"interval_reverse", "INTERVAL", "INTERVAL", "('30 days'),('1 mon')", "1 mon", "1 mon"},
		{"float4_forward", "REAL", "FLOAT4", "('0'),('-0')", "-0", "-0"},
		{"float4_reverse", "REAL", "FLOAT4", "('-0'),('0')", "0", "0"},
		{"float8_forward", "DOUBLE PRECISION", "FLOAT8", "('0'),('-0')", "-0", "-0"},
		{"float8_reverse", "DOUBLE PRECISION", "FLOAT8", "('-0'),('0')", "0", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
				referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE extrema_ties(v "+tc.ddl+")", 0))
				referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO extrema_ties VALUES "+tc.values, 2))
				value := extremaRawDatum(t, pg, tc.typ, tc.retained)
				extremaResult(t, p, conn, recorder, pg, "SELECT min(v),max(v) FROM extrema_ties", []string{"min", "max"}, []string{tc.typ, tc.typ}, [][]any{{value, value}})
				query := "SELECT min(v),max(v),min(v)::text,max(v)::text FROM extrema_ties"
				if !pg && extremaTextCastExcluded(tc.typ) {
					s := referenceError(query, "0A000")
					s.selectRows = true
					extremaBoth(t, p, conn, recorder, s)
				} else {
					// Only the first two columns are extrema. The TEXT casts are
					// scalar wrappers, whose Nullable metadata is not this contract.
					extremaResult(t, p, conn, recorder, pg, query, []string{"min", "max", "min", "max"}, []string{tc.typ, tc.typ, "TEXT", "TEXT"}, [][]any{{value, value, tc.text, tc.text}}, 2)
				}
			})
		})
	}
}

func TestReferenceExtremaBoundaries(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_boundary(v INTEGER CHECK(v>0))", 0))
		run(referenceExec("INSERT INTO extrema_boundary VALUES(1),(2)", 2))
		run(referenceExec("CREATE DOMAIN extrema_names AS NAME[]", 0))
		const tree = "{OPEXPR :opno 521 :opfuncid 147 :opresulttype 16 :opretset false :opcollid 0 :inputcollid 0 :args ({VAR :varno 1 :varattno 1 :vartype 23 :vartypmod -1 :varcollid 0 :varnullingrels (b) :varlevelsup 0 :varreturningtype 0 :varnosyn 1 :varattnosyn 1 :location -1} {CONST :consttype 23 :consttypmod -1 :constcollid 0 :constlen 4 :constbyval true :constisnull false :location -1 :constvalue 4 [ 0 0 0 0 0 0 0 0 ]}) :location -1}"
		for _, tc := range []struct {
			query, typ, message, position string
			rows                          [][]any
		}{
			{"SELECT min('{a,b}'::name[]),max('{z}'::name[]) FROM extrema_boundary", "_NAME", "min for name[] is unsupported", "8", [][]any{{[]byte("{a,b}"), []byte("{z}")}}},
			{"SELECT min(NULL::name[]),max(NULL::name[]) FROM extrema_boundary", "_NAME", "min for name[] is unsupported", "8", [][]any{{nil, nil}}},
			{"SELECT min(NULL::extrema_names),max(NULL::extrema_names) FROM extrema_boundary", "_NAME", "min for name[] is unsupported", "8", [][]any{{nil, nil}}},
			{"SELECT min(NULL::pg_node_tree),max(NULL::pg_node_tree) FROM extrema_boundary", "TEXT", "min for pg_node_tree is unsupported", "8", [][]any{{nil, nil}}},
			{"SELECT min(conbin),max(conbin) FROM pg_catalog.pg_constraint WHERE conrelid='extrema_boundary'::regclass", "TEXT", "min for pg_node_tree is unsupported", "8", [][]any{{tree, tree}}},
			{"SELECT min(1),max(2)", "INT4", "", "", [][]any{{int64(1), int64(2)}}},
			{"SELECT min(DISTINCT v),max(ALL v) FROM extrema_boundary", "INT4", "aggregate DISTINCT is unsupported", "8", [][]any{{int64(1), int64(2)}}},
			{"SELECT min(v ORDER BY v),max(v ORDER BY v DESC) FROM extrema_boundary", "INT4", "aggregate ordering is unsupported", "8", [][]any{{int64(1), int64(2)}}},
			{"SELECT min(v) FILTER(WHERE v>1),max(v) FILTER(WHERE v>1) FROM extrema_boundary", "INT4", "aggregate modifiers are unsupported", "15", [][]any{{int64(2), int64(2)}}},
			{"SELECT min(v) OVER(),max(v) OVER() FROM extrema_boundary", "INT4", "aggregate modifiers are unsupported", "15", [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}}},
		} {
			t.Run(tc.query, func(t *testing.T) {
				if pg || tc.message == "" {
					extremaResult(t, p, conn, recorder, pg, tc.query, []string{"min", "max"}, []string{tc.typ, tc.typ}, tc.rows)
				} else {
					want := controlsReferenceDiagnostic("0A000", tc.message)
					want.Position = tc.position
					extremaDiagnostic(t, p, conn, recorder, tc.query, want, "prepare")
				}
			})
		}
	})
}
