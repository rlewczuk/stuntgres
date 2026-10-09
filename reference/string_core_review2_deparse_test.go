package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

type stringCoreReview2Case struct{ name, decl, check, compact, pretty string }

// Independent PostgreSQL18.0 observations from root artifact144, all74 probes.
// These templates are captured strings, not renderer-derived expectations.
func stringCoreReview2Cases() []stringCoreReview2Case {
	var cases []stringCoreReview2Case
	for _, typ := range []struct{ name, sql, display string }{{"char", "CHAR(4)", "character(4)"}, {"varchar", "VARCHAR(4)", "character varying(4)"}, {"name", "NAME", "name"}} {
		decl := "v " + typ.sql + ",s " + typ.sql + ",f " + typ.sql + ",e TEXT"
		for _, fn := range []string{"btrim", "ltrim", "rtrim"} {
			cases = append(cases,
				stringCoreReview2Case{fn + "1_" + typ.name, decl, fn + "(v)=e", "(" + fn + "((v)::text) = e)", fn + "(v::text) = e"},
				stringCoreReview2Case{fn + "2_" + typ.name, decl, fn + "(v,s)=e", "(" + fn + "((v)::text, (s)::text) = e)", fn + "(v::text, s::text) = e"})
		}
		cases = append(cases,
			stringCoreReview2Case{"norm1_" + typ.name, decl, "pg_catalog.normalize(v)=e", "(\"normalize\"((v)::text) = e)", "\"normalize\"(v::text) = e"},
			stringCoreReview2Case{"norm2_" + typ.name, decl, "pg_catalog.normalize(v,f)=e", "(\"normalize\"((v)::text, (f)::text) = e)", "\"normalize\"(v::text, f::text) = e"},
			stringCoreReview2Case{"pred1_" + typ.name, decl, "is_normalized(v)", "is_normalized((v)::text)", "is_normalized(v::text)"},
			stringCoreReview2Case{"pred2_" + typ.name, decl, "is_normalized(v,f)", "is_normalized((v)::text, (f)::text)", "is_normalized(v::text, f::text)"},
			stringCoreReview2Case{"special_norm1_" + typ.name, decl, "normalize(v)=e", "(NORMALIZE(v) = e)", "NORMALIZE(v) = e"},
			stringCoreReview2Case{"special_norm2_" + typ.name, decl, "normalize(v,NFD)=e", "(NORMALIZE(v, NFD) = e)", "NORMALIZE(v, NFD) = e"},
			stringCoreReview2Case{"special_pred1_" + typ.name, decl, "v IS NORMALIZED", "(v IS NORMALIZED)", "(v IS NORMALIZED)"},
			stringCoreReview2Case{"special_pred2_" + typ.name, decl, "v IS NFD NORMALIZED", "(v IS NFD NORMALIZED)", "(v IS NFD NORMALIZED)"},
			stringCoreReview2Case{"literal_source_" + typ.name, "v TEXT,e TEXT", "btrim(' x '::" + typ.sql + ")=e", "(btrim((' x '::" + typ.display + ")::text) = e)", "btrim(' x '::" + typ.display + "::text) = e"},
			stringCoreReview2Case{"literal_form_" + typ.name, "v TEXT,e TEXT", "pg_catalog.normalize(v,'NFD'::" + typ.sql + ")=e", "(\"normalize\"(v, ('NFD'::" + typ.display + ")::text) = e)", "\"normalize\"(v, 'NFD'::" + typ.display + "::text) = e"})
		for _, dir := range []string{"BOTH", "LEADING", "TRAILING"} {
			cases = append(cases, stringCoreReview2Case{"special_trim_" + strings.ToLower(dir) + "_" + typ.name, decl, "trim(" + dir + " s from v)=e", "(TRIM(" + dir + " s FROM v) = e)", "TRIM(" + dir + " s FROM v) = e"})
		}
	}
	for _, fn := range []string{"btrim", "ltrim", "rtrim", "pg_catalog.normalize", "is_normalized"} {
		name, printed := fn, fn
		if fn == "pg_catalog.normalize" {
			name = "norm"
			printed = "\"normalize\""
		}
		leftCompact, leftPretty := printed+"((v)::text, s)", printed+"(v::text, s)"
		rightCompact, rightPretty := printed+"(v, (s)::text)", printed+"(v, s::text)"
		check := fn + "(v,s)"
		if fn != "is_normalized" {
			check += "=e"
			leftCompact = "(" + leftCompact + " = e)"
			leftPretty += " = e"
			rightCompact = "(" + rightCompact + " = e)"
			rightPretty += " = e"
		}
		cases = append(cases, stringCoreReview2Case{name + "_char_text", "v CHAR(4),s TEXT,e TEXT", check, leftCompact, leftPretty}, stringCoreReview2Case{name + "_text_char", "v TEXT,s CHAR(4),e TEXT", check, rightCompact, rightPretty})
	}
	for _, trim := range []struct{ name, dir string }{{"btrim", "BOTH"}, {"ltrim", "LEADING"}, {"rtrim", "TRAILING"}} {
		decl := "v BYTEA,s BYTEA,e BYTEA"
		cases = append(cases,
			stringCoreReview2Case{"bytea_ordinary_" + trim.name, decl, trim.name + "(v,s)=e", "(" + trim.name + "(v, s) = e)", trim.name + "(v, s) = e"},
			stringCoreReview2Case{"bytea_special_" + trim.name, decl, "trim(" + trim.dir + " s from v)=e", "(TRIM(" + trim.dir + " s FROM v) = e)", "TRIM(" + trim.dir + " s FROM v) = e"})
	}
	return append(cases, stringCoreReview2Case{"bytea_nested", "v BYTEA,s BYTEA,e BYTEA", "btrim(trim(LEADING s from v),s)=e", "(btrim(TRIM(LEADING s FROM v), s) = e)", "btrim(TRIM(LEADING s FROM v), s) = e"})
}

func stringCoreReview2ByteaValue(name string) []byte {
	if strings.HasSuffix(name, "ltrim") {
		return []byte("Yxx")
	}
	if strings.HasSuffix(name, "rtrim") {
		return []byte("xxY")
	}
	return []byte("Y")
}

func stringCoreReview2Reference(t *testing.T, p *referencePair) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		p.step(t, s)
	}
	for _, tc := range stringCoreReview2Cases() {
		t.Run(tc.name, func(t *testing.T) {
			run(t, referenceExec("CREATE TABLE sc2_"+tc.name+"("+tc.decl+",CONSTRAINT sc2c_"+tc.name+" CHECK("+tc.check+"))", 0))
			if strings.HasPrefix(tc.name, "bytea_") {
				run(t, referenceExec("INSERT INTO sc2_"+tc.name+" VALUES($1,$2,$3)", 1, []byte("xxYxx"), []byte("x"), stringCoreReview2ByteaValue(tc.name)))
				run(t, referenceExec("INSERT INTO sc2_"+tc.name+" VALUES(NULL,NULL,NULL)", 1))
				for _, prepared := range []bool{false, true} {
					s := referenceSelect("SELECT "+strings.TrimSuffix(tc.check, "=e")+" AS value FROM sc2_"+tc.name+" ORDER BY e", []string{"value"}, []string{"BYTEA"}, [][]any{{string(stringCoreReview2ByteaValue(tc.name))}, {nil}})
					if prepared {
						s = referencePrepared(s)
					}
					t.Run(fmt.Sprintf("value/prepared=%t", prepared), func(t *testing.T) { run(t, s) })
				}
			}
			for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
				for _, prepared := range []bool{false, true} {
					for _, consumer := range []struct{ name, q, want string }{
						{"expression", "SELECT pg_get_expr(conbin,conrelid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'", mode.want},
						{"constraint", "SELECT pg_get_constraintdef(oid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'", "CHECK (" + mode.want + ")"},
					} {
						t.Run(fmt.Sprintf("%s/%s/prepared=%t", consumer.name, mode.suffix, prepared), func(t *testing.T) {
							s := referenceSelect(consumer.q, []string{"d"}, []string{"TEXT"}, [][]any{{consumer.want}})
							if prepared {
								s = referencePrepared(s)
							}
							run(t, s)
						})
					}
				}
			}
		})
	}
	{
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
		for _, tc := range stringCoreReview2Cases() {
			for _, mode := range []struct{ suffix, want string }{{"", tc.compact}, {",false", tc.compact}, {",true", tc.pretty}} {
				for _, prepared := range []bool{false, true} {
					for _, consumer := range []struct{ q, want string }{
						{"SELECT pg_get_expr(conbin,conrelid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'", mode.want},
						{"SELECT pg_get_constraintdef(oid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='sc2c_" + tc.name + "'", "CHECK (" + mode.want + ")"},
					} {
						step := referenceSelect(consumer.q, []string{"d"}, []string{"TEXT"}, [][]any{{consumer.want}})
						if prepared {
							step = referencePrepared(step)
						}
						run(t, step)
					}
				}
			}
		}
	}
	run(t, referenceExec("CREATE TABLE sc2_group(v BYTEA,s BYTEA)", 0))
	run(t, referenceExec("INSERT INTO sc2_group VALUES($1,$2),(NULL,NULL)", 2, []byte("xxYxx"), []byte("x")))
	for _, trim := range []struct{ name, dir string }{{"btrim", "BOTH"}, {"ltrim", "LEADING"}, {"rtrim", "TRAILING"}} {
		ordinary, special := trim.name+"(v,s)", "trim("+trim.dir+" s FROM v)"
		for _, pair := range [][2]string{{ordinary, special}, {special, ordinary}} {
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT "+pair[0]+" AS value FROM sc2_group GROUP BY "+pair[1]+" ORDER BY 1", []string{"value"}, []string{"BYTEA"}, [][]any{{string(stringCoreReview2ByteaValue(trim.name))}, {nil}})
				s.checkScan = func(t *testing.T, rows *sql.Rows, row int) {
					var value []byte
					if err := rows.Scan(&value); err != nil {
						t.Fatal(err)
					}
					if row == 1 {
						if value != nil {
							t.Fatal("grouped NULL", value)
						}
					} else if !bytes.Equal(value, stringCoreReview2ByteaValue(trim.name)) {
						t.Fatal("grouped owned bytes", value)
					}
				}
				if prepared {
					s = referencePrepared(s)
				}
				t.Run(fmt.Sprintf("group/%s/%s/prepared=%t", pair[0], pair[1], prepared), func(t *testing.T) { run(t, s) })
			}
		}
	}
}

func TestReferenceStringCoreReview2Deparse(t *testing.T) {
	stringCoreReview2Reference(t, newReferenceScenario(t))
}
