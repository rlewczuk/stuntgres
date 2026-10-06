//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

type referenceCharacterNullability struct{ nullable, known bool }

func referenceCharacterMetadata(t *testing.T, p *referencePair, db *sql.DB, query string, prepared, native bool, names, types []string, lengths []int64, nullability []referenceCharacterNullability) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := db.PrepareContext(p.ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.ctx)
	} else {
		rows, err = db.QueryContext(p.ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(types) {
		t.Fatal(columns, err)
	}
	if native && len(nullability) != len(columns) {
		t.Fatal("missing independent per-column nullability", query, nullability)
	}
	check := func() {
		for i, column := range columns {
			want := lengths[i]
			if want < 0 {
				want = math.MaxInt64
				if !native && types[i] != "TEXT" {
					want = -5
				}
			}
			length, known := column.Length()
			scan := reflect.TypeFor[string]()
			if !native && types[i] == "BPCHAR" {
				scan = reflect.TypeFor[any]()
			}
			precision, scale, decimal := column.DecimalSize()
			if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || length != want || !known || column.ScanType() != scan || precision != 0 || scale != 0 || decimal {
				t.Fatalf("native=%t query=%q column=%d name=%s type=%s length=(%d,%t) scan=%v decimal=(%d,%d,%t)", native, query, i, column.Name(), column.DatabaseTypeName(), length, known, column.ScanType(), precision, scale, decimal)
			}
			value, known := column.Nullable()
			if native {
				if value != nullability[i].nullable || known != nullability[i].known {
					t.Fatal("native nullable contract", query, i, value, known, nullability[i])
				}
			} else if value || known {
				t.Fatal("pq nullable contract", value, known)
			}
		}
	}
	check()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	check()
	rows.Close()
	check()
}

func TestReferenceCharacterAliasesAndMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct {
		declaration, typ, value string
		length                  int64
	}{
		{"char", "BPCHAR", "é", 1}, {"character", "BPCHAR", "é", 1},
		{"char(3)", "BPCHAR", "é  ", 3}, {"character(3)", "BPCHAR", "é  ", 3},
		{"bpchar(3)", "BPCHAR", "é  ", 3}, {"bpchar", "BPCHAR", "é", -1},
		{"varchar", "VARCHAR", "é", -1}, {"character varying", "VARCHAR", "é", -1},
		{"varchar(3)", "VARCHAR", "é", 3}, {"character varying(3)", "VARCHAR", "é", 3},
		{"text", "TEXT", "é", -1}, {`"bpchar"`, "BPCHAR", "é", -1}, {`"varchar"(3)`, "VARCHAR", "é", 3},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			p.step(t, referenceExec("CREATE TABLE aliases(v "+tc.declaration+")", 0))
			for _, prepared := range []bool{false, true} {
				referenceCharacterMetadata(t, p, p.memory, "SELECT v FROM aliases", prepared, true, []string{"v"}, []string{tc.typ}, []int64{tc.length}, []referenceCharacterNullability{{true, true}})
				referenceCharacterMetadata(t, p, p.postgres, "SELECT v FROM aliases", prepared, false, []string{"v"}, []string{tc.typ}, []int64{tc.length}, nil)
			}
			p.step(t, referenceExec("INSERT INTO aliases VALUES('é'),(NULL)", 2))
			s := referenceSelect("SELECT v FROM aliases", []string{"v"}, []string{tc.typ}, [][]any{{tc.value}, {nil}})
			s.bag = true
			p.step(t, s)
			p.step(t, referencePrepared(s))
			p.step(t, referenceExec("DROP TABLE aliases", 0))
		})
	}
	for _, declaration := range []string{"char(10485760)", "varchar(10485760)"} {
		p.step(t, referenceExec("CREATE TABLE maximum(v "+declaration+")", 0))
		p.step(t, referenceExec("DROP TABLE maximum", 0))
	}
	p.step(t, referenceExec("CREATE TABLE cast_names(c CHAR(3),v VARCHAR(3),t TEXT)", 0))
	columnQuery := "SELECT CAST(c AS varchar(2)),CAST(v AS char(4)),CAST(t AS text),c::char(4)::text,CAST(CAST(v AS char(2)) AS varchar(3)) FROM cast_names"
	for _, prepared := range []bool{false, true} {
		names := []string{"c", "v", "t", "c", "v"}
		types := []string{"VARCHAR", "BPCHAR", "TEXT", "TEXT", "VARCHAR"}
		lengths := []int64{2, 4, -1, -1, 3}
		// Only CAST(t AS text) is identical Usage and retains the physical
		// column proof. Resizing/retyping casts remain genuine expressions.
		nullability := []referenceCharacterNullability{{}, {}, {true, true}, {}, {}}
		referenceCharacterMetadata(t, p, p.memory, columnQuery, prepared, true, names, types, lengths, nullability)
		referenceCharacterMetadata(t, p, p.postgres, columnQuery, prepared, false, names, types, lengths, nil)
	}
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	q := "SELECT CAST(NULL AS char(3)),CAST(NULL AS varchar(2)),CAST(NULL AS text),CAST(NULL AS bpchar),CAST(NULL AS varchar) FROM seed"
	names := []string{"bpchar", "varchar", "text", "bpchar", "varchar"}
	types := []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}
	lengths := []int64{3, 2, -1, -1, -1}
	for _, prepared := range []bool{false, true} {
		referenceCharacterMetadata(t, p, p.memory, q, prepared, true, names, types, lengths, make([]referenceCharacterNullability, len(types)))
		referenceCharacterMetadata(t, p, p.postgres, q, prepared, false, names, types, lengths, nil)
		s := referenceSelect(q, names, types, [][]any{{nil, nil, nil, nil, nil}})
		if prepared {
			s = referencePrepared(s)
		}
		p.step(t, s)
	}
}

func TestReferenceCharacterCastIdentityMetadata(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE proof(c CHAR(3) NOT NULL,v VARCHAR(3),t TEXT NOT NULL,u BPCHAR,n CHAR(3))", 0))
	q := "SELECT c::char(3),c::char(4),v::varchar(3),v::varchar(2),CAST(t AS text),u::bpchar,n::char(3),c::char(3)::char(3),c::char(4)::char(4) FROM proof"
	names := []string{"c", "c", "v", "v", "t", "u", "n", "c", "c"}
	types := []string{"BPCHAR", "BPCHAR", "VARCHAR", "VARCHAR", "TEXT", "BPCHAR", "BPCHAR", "BPCHAR", "BPCHAR"}
	lengths := []int64{3, 4, 3, 2, -1, -1, 3, 3, 4}
	// Identity casts preserve the physical nullable/not-null proof. Typmod
	// changes, including a nested identity of a changed expression, do not.
	nullability := []referenceCharacterNullability{{false, true}, {}, {true, true}, {}, {false, true}, {true, true}, {true, true}, {false, true}, {}}
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO proof VALUES('a','b  ','tail ','u  ',NULL)", 1))
		}
		for _, prepared := range []bool{false, true} {
			referenceCharacterMetadata(t, p, p.memory, q, prepared, true, names, types, lengths, nullability)
			referenceCharacterMetadata(t, p, p.postgres, q, prepared, false, names, types, lengths, nil)
			var values [][]any
			if populated {
				values = [][]any{{"a  ", "a   ", "b  ", "b ", "tail ", "u  ", nil, "a  ", "a   "}}
			}
			s := referenceSelect(q, names, types, values)
			if prepared {
				s = referencePrepared(s)
			}
			p.step(t, s)
		}
	}
}
