//go:build reference

package reference_test

import "testing"

func TestReferenceCharacterHeldPreparedCastsAndRightColumn(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE c(c CHAR(2),v VARCHAR(4),t TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES('a','a ','a '),('b','b ','b ')", 2))
	for _, predicate := range []string{"CAST('a ' AS bpchar)=c", "'a    '=c", "CAST('a' AS text)=c"} {
		s := referenceSelect("SELECT c FROM c WHERE "+predicate, []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct {
		arg  any
		rows [][]any
	}{{"a   ", [][]any{{"a "}}}, {"b", [][]any{{"b "}}}, {nil, nil}} {
		s := referenceSelect("SELECT c FROM c WHERE $1=c", []string{"c"}, []string{"BPCHAR"}, tc.rows, tc.arg)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	s := referenceSelect("SELECT c::char(4)::text,CAST(v AS char(3)),CAST(t AS varchar(1)) FROM c", []string{"c", "v", "t"}, []string{"TEXT", "BPCHAR", "VARCHAR"}, [][]any{{"a", "a  ", "a"}, {"b", "b  ", "b"}})
	s.bag = true
	p.step(t, s)
	p.step(t, referencePrepared(s))
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	query := "SELECT CAST($1 AS varchar(2)),CAST($2 AS char(3)) FROM seed"
	for _, backend := range []struct {
		name   string
		native bool
	}{{"memory", true}, {"postgres", false}} {
		t.Run(backend.name, func(t *testing.T) {
			db := p.postgres
			if backend.native {
				db = p.memory
			}
			stmt, err := db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				in             any
				varying, fixed any
			}{
				{"é🙂x", "é🙂", "é🙂x"}, {"a", "a", "a  "}, {"", "", "   "}, {nil, nil, nil},
			} {
				rows, err := stmt.QueryContext(p.ctx, tc.in, tc.in)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 2 {
					rows.Close()
					t.Fatal(columns, err)
				}
				for i, expected := range []struct {
					name, typ string
					length    int64
				}{{"varchar", "VARCHAR", 2}, {"bpchar", "BPCHAR", 3}} {
					length, known := columns[i].Length()
					if columns[i].Name() != expected.name || columns[i].DatabaseTypeName() != expected.typ || !known || length != expected.length {
						rows.Close()
						t.Fatal(columns[i], length, known)
					}
				}
				var got [][]any
				for rows.Next() {
					var a, b any
					if err := rows.Scan(&a, &b); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{referenceNormalize(a), referenceNormalize(b)})
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				rows.Close()
				if _, matched := referenceCompareRows(got, [][]any{{tc.varying, tc.fixed}}, false); !matched {
					t.Fatal(got, tc)
				}
			}
		})
	}
	p.step(t, referenceExec("CREATE TABLE io(c CHAR(5),v VARCHAR(5))", 0))
	for _, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO io VALUES(TRUE,123),(FALSE,1.25)", 2)
		if prepared {
			s = referencePrepared(s)
		}
		p.step(t, s)
	}
	io := referenceSelect("SELECT * FROM io", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"true ", "123"}, {"false", "1.25"}, {"true ", "123"}, {"false", "1.25"}})
	io.bag = true
	p.step(t, io)
}
