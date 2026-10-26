package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 create_procedure.sql ptest6a/6b and
// functioncmds.c CallStmtResultDesc: declaration names, invocation member types.
// The same roster and checker serve ordinary execution and every replay phase.
type proceduresPolyShape struct {
	name, formals, sqlBody, plBody                  string
	names                                           []string
	catalogNames, modes, allTypes, inputs, identity string
}

var proceduresPolyShapes = []proceduresPolyShape{
	{
		name: "sole", formals: "IN x anyelement,OUT answer anyelement",
		sqlBody: "SELECT x AS body_alias", plBody: "BEGIN answer:=x;END",
		names: []string{"answer"}, catalogNames: "{x,answer}", modes: "{i,o}",
		allTypes: "{2283,2283}", inputs: "2283", identity: "anyelement",
	},
	{
		name: "multiple", formals: "IN x anyelement,OUT first anyelement,OUT second anyelement",
		sqlBody: "SELECT x AS ignored_second,x AS ignored_first", plBody: "BEGIN first:=x;second:=x;END",
		names: []string{"first", "second"}, catalogNames: "{x,first,second}", modes: "{i,o,o}",
		allTypes: "{2283,2283,2283}", inputs: "2283", identity: "anyelement",
	},
	{
		name: "inout", formals: "INOUT a anyelement,OUT b anyelement",
		sqlBody: "SELECT a AS ignored_b,a AS ignored_a", plBody: "BEGIN b:=a;END",
		names: []string{"a", "b"}, catalogNames: "{a,b}", modes: "{b,o}",
		allTypes: "{2283,2283}", inputs: "2283", identity: "anyelement",
	},
	{
		name: "array", formals: "IN x anyarray,OUT answer anyarray",
		sqlBody: "SELECT x AS body_alias", plBody: "BEGIN answer:=x;END",
		names: []string{"answer"}, catalogNames: "{x,answer}", modes: "{i,o}",
		allTypes: "{2277,2277}", inputs: "2277", identity: "anyarray",
	},
	{
		name: "array_inout", formals: "INOUT a anyarray,OUT b anyarray",
		sqlBody: "SELECT a AS ignored_b,a AS ignored_a", plBody: "BEGIN b:=a;END",
		names: []string{"a", "b"}, catalogNames: "{a,b}", modes: "{b,o}",
		allTypes: "{2277,2277}", inputs: "2277", identity: "anyarray",
	},
	{
		name: "mixed", formals: "IN x anyelement,IN xs anyarray,OUT answer anyelement,OUT items anyarray",
		sqlBody: "SELECT x AS ignored_items,xs AS ignored_answer", plBody: "BEGIN answer:=x;items:=xs;END",
		names: []string{"answer", "items"}, catalogNames: "{x,xs,answer,items}", modes: "{i,i,o,o}",
		allTypes: "{2283,2277,2283,2277}", inputs: "2283 2277", identity: "anyelement,anyarray",
	},
}

type proceduresPolyCall struct {
	query  string
	types  []string
	inputs [][]any
	rows   [][][]any
}

func proceduresPolyName(shape proceduresPolyShape, language string) string {
	return "pc_poly_" + shape.name + "_" + language
}

func proceduresPolySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, shape proceduresPolyShape, language string) {
	t.Helper()
	body := shape.sqlBody
	if language == "plpgsql" {
		body = shape.plBody
	}
	resultsExec(t, p, b, c, "CREATE PROCEDURE "+proceduresPolyName(shape, language)+"("+shape.formals+") LANGUAGE "+language+" AS $$"+body+"$$")
}

func proceduresPolyCalls(shape proceduresPolyShape, language string) []proceduresPolyCall {
	name := proceduresPolyName(shape, language)
	var calls []proceduresPolyCall
	add := func(cast, typ string, inputs [][]any, outputs []any) {
		q := "CALL " + name + "($1::" + cast + strings.Repeat(",NULL", len(shape.names)) + ")"
		if strings.Contains(shape.name, "inout") {
			q = "CALL " + name + "($1::" + cast + ",NULL)"
		}
		types := make([]string, len(shape.names))
		rows := make([][][]any, len(outputs))
		for i := range types {
			types[i] = typ
		}
		for i, value := range outputs {
			row := make([]any, len(shape.names))
			for j := range row {
				row[j] = value
			}
			rows[i] = [][]any{row}
		}
		calls = append(calls, proceduresPolyCall{q, types, inputs, rows})
	}
	switch shape.name {
	case "mixed":
		calls = append(calls, proceduresPolyCall{
			query:  "CALL " + name + "($1::integer,$2::integer[],NULL,NULL)",
			types:  []string{"INT4", "_INT4"},
			inputs: [][]any{{7, "{1,NULL,3}"}, {8, "[0:1]={8,9}"}, {nil, nil}},
			rows:   [][][]any{{{int64(7), "{1,NULL,3}"}}, {{int64(8), "[0:1]={8,9}"}}, {{nil, nil}}},
		})
	case "array", "array_inout":
		add("integer[]", "_INT4", [][]any{{"{1,NULL,3}"}, {"{}"}, {nil}, {"[0:1]={8,9}"}}, []any{"{1,NULL,3}", "{}", nil, "[0:1]={8,9}"})
	default:
		first := int64(7)
		if shape.name == "inout" {
			first = 1 // Released ptest6a's exact integer witness.
		}
		add("integer", "INT4", [][]any{{first}, {8}, {nil}}, []any{first, int64(8), nil})
		add("bigint", "INT8", [][]any{{int64(9)}, {int64(10)}, {nil}}, []any{int64(9), int64(10), nil})
		word := "word"
		if shape.name == "multiple" {
			word = "pair"
		}
		add("text", "TEXT", [][]any{{word}, {"changed"}, {nil}}, []any{word, "changed", nil})
	}
	return calls
}

func proceduresPolyCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, shape proceduresPolyShape, language string) {
	t.Helper()
	resultsBothMetadata(t, p, b, c,
		"SELECT prokind::text AS kind,prorettype::bigint AS result,proargnames::text AS names,proargmodes::text AS modes,proallargtypes::text AS types,proargtypes::text AS inputs FROM pg_proc WHERE proname='"+proceduresPolyName(shape, language)+"'",
		[]string{"kind", "result", "names", "modes", "types", "inputs"},
		[]string{"TEXT", "INT8", "TEXT", "TEXT", "TEXT", "TEXT"},
		[][]any{{"p", int64(2249), shape.catalogNames, shape.modes, shape.allTypes, shape.inputs}},
		[]resultsNativeColumn{resultsTextMetadata, {}, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata})
}

// Retained native rows are eager/detached. The ordinary shared routes below
// still exercise pq streaming; no second backend-specific scenario is defined.
func proceduresPolyRetain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, call proceduresPolyCall, names []string) *sql.Rows {
	t.Helper()
	if b.PG {
		return nil
	}
	rows, err := c.QueryContext(p.Ctx, call.query, call.inputs[0]...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	proceduresMetadata(b, names, call.types)(t, rows)
	return rows
}

func proceduresPolyReadRetained(t *testing.T, b fixture.Backend, rows *sql.Rows, call proceduresPolyCall, names []string) {
	t.Helper()
	if rows == nil {
		return
	}
	defer rows.Close()
	proceduresMetadata(b, names, call.types)(t, rows)
	if !rows.Next() {
		t.Fatal("lost retained polymorphic row", rows.Err())
	}
	values := make([]any, len(names))
	dest := make([]any, len(names))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, value := range values {
		fixture.AssertRawValue(t, b.PG, call.types[i], value)
		values[i] = fixture.Normalize(value)
	}
	if !reflect.DeepEqual(values, call.rows[0][0]) {
		t.Errorf("retained values %v want %v", values, call.rows[0][0])
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained row count/EOF", rows.Err())
	}
}

func proceduresPolyCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, shape proceduresPolyShape, language string) {
	t.Helper()
	calls := proceduresPolyCalls(shape, language)
	for _, call := range calls {
		for i, args := range call.inputs {
			proceduresBoth(t, p, b, c, call.query, shape.names, call.types, call.rows[i], args...)
		}
		proceduresHeld(t, p, b, c, call.query, shape.names, call.types, call.inputs, call.rows)
	}
	// All held statements remain open together. Fixed inferred SQL types do
	// not change; the declaration's last specialization does integer->text->integer.
	statements := make([]*sql.Stmt, len(calls))
	for i, call := range calls {
		stmt, err := c.PrepareContext(p.Ctx, call.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		statements[i] = stmt
	}
	retained := proceduresPolyRetain(t, p, b, c, calls[0], shape.names)
	order := []int{0, 0}
	if len(calls) == 3 {
		order = []int{0, 2, 0, 1, 2, 0}
	}
	for _, index := range order {
		call := calls[index]
		held := fixture.HeldStatement{Executor: c, Statement: statements[index], SQL: call.query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, call.query, shape.names, call.types, call.rows[1], call.inputs[1]...))
	}
	proceduresPolyReadRetained(t, b, retained, calls[0], shape.names)
	if shape.name == "multiple" {
		proceduresBoth(t, p, b, c, "CALL "+proceduresPolyName(shape, language)+"(second=>NULL,x=>7::integer,first=>NULL)", shape.names, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
	}
	// Specialization must not rewrite the immutable pseudo-type declaration.
	proceduresPolyCatalog(t, p, b, c, shape, language)
}

func TestReferenceRoutineProceduresPolymorphicOutputs(t *testing.T) {
	for _, shape := range proceduresPolyShapes {
		for _, language := range []string{"sql", "plpgsql"} {
			t.Run(shape.name+"/"+language, func(t *testing.T) {
				proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					proceduresPolySetup(t, p, b, c, shape, language)
					proceduresPolyCheck(t, p, b, c, shape, language)
				})
			})
		}
	}
}

func TestReferenceRoutineProceduresPolymorphicPersistence(t *testing.T) {
	for _, shape := range proceduresPolyShapes {
		for _, language := range []string{"sql", "plpgsql"} {
			t.Run(shape.name+"/"+language, func(t *testing.T) {
				p := fixture.NewScenario(t)
				// PG runs first without a native filter, so a genuine native red
				// cannot prevent observing this leaf's independent oracle contract.
				backends := p.Backends()
				for i := len(backends) - 1; i >= 0; i-- {
					b := backends[i]
					t.Run(b.Name, func(t *testing.T) {
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						proceduresPolySetup(t, p, b, c, shape, language)
						check := func(conn *sql.Conn, backend fixture.Backend) {
							proceduresPolyCheck(t, p, backend, conn, shape, language)
						}
						check(c, b)
						if b.PG {
							return // Native controls have no PostgreSQL API counterpart.
						}
						call := proceduresPolyCalls(shape, language)[0]
						retained := proceduresPolyRetain(t, p, b, c, call, shape.names)
						var emitted []byte
						for range 2 {
							emitted = resultsNativeReplayRound(t, p, b, c, check)
							if p.Postgres != nil {
								proceduresPolyReplayPG(t, p, emitted, check)
							}
						}
						connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
						if err != nil {
							t.Fatal(err)
						}
						raw := sql.OpenDB(connector)
						defer raw.Close()
						recorded, rec := fixture.RecordedNativePool(t, connector)
						if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
							t.Fatal(err)
						}
						clone, err := recorded.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer clone.Close()
						cloneBackend := fixture.Backend{Recorder: rec}
						check(clone, cloneBackend)
						resultsExec(t, p, cloneBackend, clone, "DROP PROCEDURE "+proceduresPolyName(shape, language)+"("+shape.identity+")")
						proceduresCount(t, p, cloneBackend, clone, "pg_proc WHERE proname='"+proceduresPolyName(shape, language)+"'", 0)
						check(c, b) // Destination mutation cannot change source ownership.
						proceduresPolyReadRetained(t, b, retained, call, shape.names)
					})
				}
			})
		}
	}
}

func proceduresPolyReplayPG(t *testing.T, p *fixture.Pair, emitted []byte, check func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	pg, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, q := range []string{
		"DROP SCHEMA public CASCADE;CREATE SCHEMA public;GRANT USAGE ON SCHEMA public TO PUBLIC",
		string(emitted), // Actual emitted SQL, unchanged; keep bootstrap owner directives.
		"RESET SESSION AUTHORIZATION;SET check_function_bodies=true",
	} {
		if _, err := pg.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(fmt.Errorf("polymorphic emitted SQL replay: %w", err))
		}
	}
	check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
	if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
}
