//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// These complete expectations are independent root observations from the pinned
// PostgreSQL 18.0 UTF8/C capture, not outputs learned from the native engine.
// Utility semantic errors occur at Execute; utility syntax errors occur at
// Prepare. This entrypoint never executes native SQL.
func characterOracleDiagnostic(t *testing.T, query, stage string, err error, want referenceDiagnostic) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	if err == nil {
		t.Fatalf("query=%q stage=%s unexpectedly succeeded; want %#v", query, stage, want)
	}
	got := referenceExtractDiagnostic(t, err)
	t.Logf("query=%q stage=%s diagnostic=%#v", query, stage, got)
	referenceAssertDiagnostic(t, err, &want)
}

func characterOracleExec(t *testing.T, p *referencePair, query string, prepared bool, prepareError bool, want referenceDiagnostic, args ...any) {
	t.Helper()
	var err error
	stage := "direct-execute"
	if prepared {
		stmt, e := p.postgres.PrepareContext(p.ctx, query)
		if prepareError {
			if stmt != nil {
				stmt.Close()
				t.Fatal("failed Prepare exposed a statement")
			}
			characterOracleDiagnostic(t, query, "prepare", e, want)
			return
		}
		if e != nil {
			t.Fatalf("query=%q unexpected Prepare error: %v", query, e)
		}
		defer stmt.Close()
		_, err = stmt.ExecContext(p.ctx, args...)
		stage = "prepared-execute"
	} else {
		_, err = p.postgres.ExecContext(p.ctx, query, args...)
	}
	if want.Code == "" {
		if err != nil {
			t.Fatalf("query=%q stage=%s unexpected error: %v", query, stage, err)
		}
		return
	}
	characterOracleDiagnostic(t, query, stage, err, want)
}

func TestReferenceCharacterOracle(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{
		"CREATE TABLE seed(i INT)", "INSERT INTO seed VALUES(1)",
		"CREATE TABLE inputs(t TEXT,c CHAR(2),v VARCHAR(2),u BPCHAR)",
		"CREATE TABLE mixed(c CHAR(3),d CHAR(5),v VARCHAR(5),t TEXT,u BPCHAR)",
		"INSERT INTO mixed VALUES('a','a','a ','a ','a  ')",
		"CREATE TABLE widths(a CHAR(2),b CHAR(4))",
		"CREATE TABLE cast_names(c CHAR(3),v VARCHAR(3),t TEXT)",
	} {
		if _, err := p.postgres.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ decl, code, message string }{
		{"char(0)", "22023", "length for type char must be at least 1"},
		{"varchar(0)", "22023", "length for type varchar must be at least 1"},
		{"char(10485761)", "22023", "length for type char cannot exceed 10485760"},
		{"varchar(10485761)", "22023", "length for type varchar cannot exceed 10485760"},
		{"bpchar(-1)", "22023", "length for type char must be at least 1"},
		{`"varchar"(-1)`, "22023", "length for type varchar must be at least 1"},
		{"text(3)", "42601", `type modifier is not allowed for type "text"`},
	} {
		t.Run("known_modifier_"+tc.decl, func(t *testing.T) {
			q := "/*é🙂*/ CREATE TABLE invalid(v " + tc.decl + ")"
			want := referenceDiagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.decl)]) + 1)}
			characterOracleExec(t, p, q, false, false, want)
			// Root's second pinned18 capture confirms all these utility
			// semantic errors accept Prepare and fail only at Execute.
			characterOracleExec(t, p, q, true, false, want)
		})
	}
	for _, base := range []string{"char", "character", "varchar", "character varying", "bpchar", `"bpchar"`, `"varchar"`} {
		for _, mod := range []string{"-1", "+2", "2,3", "2.5", "'2'", "'oops'", "2147483648"} {
			t.Run("modifier_"+base+"("+mod+")", func(t *testing.T) {
				q := "/*é🙂*/ CREATE TABLE modifier_capture(v " + base + "(" + mod + "))"
				generic := base == "bpchar" || strings.HasPrefix(base, `"`)
				want := referenceDiagnostic{}
				if !generic {
					token := mod
					if mod == "-1" {
						token = "-"
					}
					if mod == "+2" {
						token = "+"
					}
					if mod == "2,3" {
						token = ","
					}
					// The first '(' belongs to the column list; locate the modifier.
					position := utf8.RuneCountInString(q[:strings.LastIndex(q, "(")+1]) + 1
					if mod == "2,3" {
						position++
					}
					want = referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: strconv.Itoa(position)}
				} else {
					position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, base)]) + 1)
					switch mod {
					case "-1":
						name := "char"
						if base == `"varchar"` {
							name = "varchar"
						}
						want = referenceDiagnostic{Code: "22023", Message: "length for type " + name + " must be at least 1", Position: position}
					case "+2":
						want = referenceDiagnostic{Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: position}
					case "2,3":
						want = referenceDiagnostic{Code: "22023", Message: "invalid type modifier", Position: position}
					case "2.5":
						want = referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "2.5"`, Position: position}
					case "'oops'":
						want = referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: position}
					case "2147483648":
						want = referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: position}
					case "'2'": // Independently observed accepted generic constant.
					}
				}
				for _, prepared := range []bool{false, true} {
					characterOracleExec(t, p, q, prepared, !generic, want)
					if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE IF EXISTS modifier_capture"); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
	t.Run("assignment_stages", func(t *testing.T) {
		for _, q := range []string{"INSERT INTO inputs(c) VALUES('abc')", "INSERT INTO inputs(c) VALUES(CAST('abcd' AS varchar(3)))", "INSERT INTO inputs(c) VALUES($1)"} {
			args := []any{}
			if strings.Contains(q, "$1") {
				args = []any{"abc"}
			}
			want := referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"}
			for _, prepared := range []bool{false, true} {
				characterOracleExec(t, p, q, prepared, false, want, args...)
			}
		}
	})
	t.Run("invalid_bind", func(t *testing.T) {
		for _, column := range []string{"t", "c", "v", "u"} {
			q := "INSERT INTO inputs(" + column + ") VALUES($1)"
			for _, tc := range []struct {
				payload []byte
				bytes   string
			}{
				{[]byte{'a', 0, 'b'}, "0x00"}, {[]byte{0xff}, "0xff"}, {[]byte{0xc3, 0x28}, "0xc3 0x28"}, {[]byte{0xe2, 0x82}, "0xe2 0x82"},
			} {
				for _, byteArg := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s_%x_bytes%t_prepared%t", column, tc.payload, byteArg, prepared), func(t *testing.T) {
							var arg any = string(tc.payload)
							if byteArg {
								arg = tc.payload
							}
							want := referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes, Context: "unnamed portal parameter $1"}
							characterOracleExec(t, p, q, prepared, false, want, arg)
						})
					}
				}
			}
		}
	})
	t.Run("sql_zero_escapes", func(t *testing.T) {
		for _, tc := range []struct {
			expr string
			want referenceDiagnostic
		}{
			{`E'\000'`, referenceDiagnostic{Code: "22021", Message: `invalid byte sequence for encoding "UTF8": 0x00`}},
			{`U&'\0000'`, referenceDiagnostic{Code: "42601", Message: "invalid Unicode escape value", Position: "33"}},
		} {
			for _, prepared := range []bool{false, true} {
				characterOracleExec(t, p, "INSERT INTO inputs(t) VALUES("+tc.expr+")", prepared, true, tc.want)
			}
		}
	})
	t.Run("metadata", func(t *testing.T) {
		for _, tc := range []struct {
			query        string
			names, types []string
			lengths      []int64
		}{
			{"SELECT c,v,t,u FROM inputs", []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, []int64{2, 2, math.MaxInt64, -5}},
			{"SELECT CAST(NULL AS char(3)),CAST(NULL AS varchar(2)),CAST(NULL AS text),CAST(NULL AS bpchar),CAST(NULL AS varchar) FROM seed", []string{"bpchar", "varchar", "text", "bpchar", "varchar"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR", "VARCHAR"}, []int64{3, 2, math.MaxInt64, -5, -5}},
			{"SELECT CAST(c AS varchar(2)),CAST(v AS char(4)),CAST(t AS text),c::char(4)::text,CAST(CAST(v AS char(2)) AS varchar(3)) FROM cast_names", []string{"c", "v", "t", "c", "v"}, []string{"VARCHAR", "BPCHAR", "TEXT", "TEXT", "VARCHAR"}, []int64{2, 4, math.MaxInt64, math.MaxInt64, 3}},
		} {
			rows, err := p.postgres.QueryContext(p.ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(types) != len(tc.names) {
				t.Fatal("metadata count", len(types))
			}
			check := func(stage string) {
				for i, typ := range types {
					n, known := typ.Length()
					nullable, nk := typ.Nullable()
					precision, scale, dk := typ.DecimalSize()
					scan := reflect.TypeFor[string]()
					if tc.types[i] == "BPCHAR" {
						scan = reflect.TypeFor[any]()
					}
					t.Logf("query=%q stage=%s index=%d name=%q type=%s length=(%d,%t) scan=%v nullable=(%t,%t) decimal=(%d,%d,%t)", tc.query, stage, i, typ.Name(), typ.DatabaseTypeName(), n, known, typ.ScanType(), nullable, nk, precision, scale, dk)
					if typ.Name() != tc.names[i] || typ.DatabaseTypeName() != tc.types[i] || n != tc.lengths[i] || !known || typ.ScanType() != scan || nullable || nk || precision != 0 || scale != 0 || dk {
						t.Fatalf("query=%q stage=%s column=%d metadata mismatch", tc.query, stage, i)
					}
				}
			}
			check("open")
			count := 0
			for rows.Next() {
				values := make([]any, len(types))
				dest := make([]any, len(types))
				for i := range dest {
					dest[i] = &values[i]
				}
				if err = rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				for _, value := range values {
					if value != nil {
						t.Fatal("NULL cast exported a non-NULL value", value)
					}
				}
				count++
			}
			wantCount := 0
			if strings.Contains(tc.query, "CAST(NULL") {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatal("metadata query row count", count, wantCount)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			check("eof")
			rows.Close()
			check("closed")
		}
	})
	t.Run("mixed_resolution", func(t *testing.T) {
		for _, tc := range []struct {
			predicate string
			match     bool
		}{
			{"c=d", true}, {"d=c", true}, {"c=t", false}, {"t=c", false}, {"c=v", true}, {"v=c", true}, {"u=v", true}, {"v=u", true},
			{"c='a    '", true}, {"'a    '=c", true}, {"c=CAST('a ' AS text)", false}, {"CAST('a ' AS text)=c", false}, {"v='a'", false}, {"t='a'", false},
		} {
			q := "SELECT c,v,t,u FROM mixed WHERE " + tc.predicate
			var want [][]any
			if tc.match {
				want = [][]any{{"a  ", "a ", "a ", "a  "}}
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, []string{"c", "v", "t", "u"}, []string{"BPCHAR", "VARCHAR", "TEXT", "BPCHAR"}, want))
		}
	})
	t.Run("held_parameters", func(t *testing.T) {
		s, err := p.postgres.PrepareContext(p.ctx, "INSERT INTO widths VALUES($1,$1)")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for _, value := range []any{"a", nil} {
			if _, err = s.ExecContext(p.ctx, value); err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.ExecContext(p.ctx, "abc")
		characterOracleDiagnostic(t, "INSERT INTO widths VALUES($1,$1)", "prepared-execute", err, referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"})
		widths := referenceSelect("SELECT * FROM widths", []string{"a", "b"}, []string{"BPCHAR", "BPCHAR"}, [][]any{{"a ", "a   "}, {nil, nil}})
		widths.bag = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, widths)
		cast, err := p.postgres.PrepareContext(p.ctx, "SELECT CAST($1 AS varchar(2)) FROM seed")
		if err != nil {
			t.Fatal(err)
		}
		defer cast.Close()
		for _, tc := range []struct{ in, want any }{{"é🙂x", "é🙂"}, {"", ""}, {nil, nil}} {
			var got sql.NullString
			if err = cast.QueryRowContext(p.ctx, tc.in).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if got.Valid {
					t.Fatal(got)
				}
			} else if !got.Valid || got.String != tc.want {
				t.Fatal(got, tc.want)
			}
		}
	})
}
