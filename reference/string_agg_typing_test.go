package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceStringAggTyping(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE DOMAIN sa_text AS TEXT", 0))
			run(referenceExec("CREATE DOMAIN sa_binary AS BYTEA", 0))
			run(referenceExec("CREATE TABLE typed(id INT,v VARCHAR(8),c CHAR(4),n NAME,t sa_text,b sa_binary)", 0))
			run(referenceExec(`INSERT INTO typed VALUES(2,'b','b','b','b','\xff'),(1,'a','a','a','a','\x00')`, 2))
			for _, s := range []referenceStep{
				referenceSelect("SELECT string_agg(v,',' ORDER BY id),string_agg(c,',' ORDER BY id),string_agg(n,',' ORDER BY id),string_agg(t,',' ORDER BY id),string_agg(b,'\\x80' ORDER BY id) FROM typed", []string{"string_agg", "string_agg", "string_agg", "string_agg", "string_agg"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "BYTEA"}, [][]any{{"a,b", "a,b", "a,b", "a,b", string([]byte{0, 128, 255})}}),
				referenceSelect("SELECT string_agg(NULL,NULL) FROM typed", []string{"string_agg"}, []string{"TEXT"}, [][]any{{nil}}),
				referenceSelect("SELECT string_agg(NULL,b ORDER BY id),string_agg(b,NULL ORDER BY id) FROM typed", []string{"string_agg", "string_agg"}, []string{"BYTEA", "BYTEA"}, [][]any{{nil, string([]byte{0, 255})}}),
				referenceSelect("SELECT string_agg($1,$2 ORDER BY id) FROM typed", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"x/x"}}, "x", "/"),
				referenceSelect("SELECT string_agg($1,b ORDER BY id) FROM typed", []string{"string_agg"}, []string{"BYTEA"}, [][]any{{string([]byte{128, 255, 128})}}, []byte{128}),
				referenceSelect("SELECT string_agg(b,$1 ORDER BY id) FROM typed", []string{"string_agg"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 92, 255})}}, []byte{92}),
				referenceSelect("SELECT string_agg($1,$1 ORDER BY id) FROM typed", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"xxx"}}, "x"),
			} {
				s.checkMetadata = stringAggMetadata(backend.pg)
				run(s)
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE required(v TEXT NOT NULL,b BYTEA NOT NULL)", 0))
			s := referenceSelect("SELECT string_agg(v,','),string_agg(b,'') FROM required", []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{nil, nil}})
			s.checkMetadata = stringAggMetadata(backend.pg)
			run(s)
		})
	}
}

func TestReferenceStringAggBinaryOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE ownership(id INT,b BYTEA)", 0))
			run(referenceExec(`INSERT INTO ownership VALUES(1,'\x00'),(2,'\xff')`, 2))
			input := []byte{128}
			rows, err := backend.db.QueryContext(p.ctx, "SELECT string_agg(b,$1 ORDER BY id) FROM ownership", input)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			input[0] = 1
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var cell any
			if err := rows.Scan(&cell); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cell, []byte{0, 128, 255}) {
				t.Fatalf("captured bind=%#v", cell)
			}
			cell.([]byte)[0] = 9
			var again []byte
			if err := rows.Scan(&again); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(again, []byte{0, 128, 255}) {
				t.Fatalf("export aliases=%v", again)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			run(referenceSelect("SELECT string_agg(b,$1 ORDER BY id) FROM ownership", []string{"string_agg"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 1, 255})}}, input))
		})
	}
}
