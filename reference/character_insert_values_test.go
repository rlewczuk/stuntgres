//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
)

// Independently release-source-derived expectations confirmed by root's pinned18
// capture before production classification changes, not native outputs.
func characterSourceValuesWitnesses(t *testing.T, postgresOnly bool) {
	p := newReferencePair(t)
	run := func(s referenceStep) {
		t.Helper()
		if postgresOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b  ' AS text)", "CAST('a ' AS varchar)", [][]any{{"a "}, {"b  "}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS text)", [][]any{{"a "}, {"b  "}}},
		{"CAST('b  ' AS text)", "CAST('a ' AS char(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a ' AS char(2))", "CAST('b  ' AS text)", [][]any{{"a"}, {"b"}}},
		{"CAST('b  ' AS char(3))", "CAST('a ' AS varchar)", [][]any{{"a"}, {"b"}}},
		{"CAST('a ' AS varchar)", "CAST('b  ' AS char(3))", [][]any{{"a "}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b ' AS varchar(2))", "CAST('a ' AS varchar(2))", [][]any{{"a "}, {"b "}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.first+"/"+tc.second+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				run(referenceExec("CREATE TABLE dst(v TEXT)", 0))
				q := "INSERT INTO dst VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
				s := referenceExec(q, 2)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
				run(referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, tc.want))
				if !postgresOnly {
					referenceCharacterMetadata(t, p, p.memory, "SELECT v FROM dst", prepared, true, []string{"v"}, []string{"TEXT"}, []int64{-1}, []referenceCharacterNullability{{true, true}})
				}
				referenceCharacterMetadata(t, p, p.postgres, "SELECT v FROM dst", prepared, false, []string{"v"}, []string{"TEXT"}, []int64{-1}, nil)
				t.Logf("query=%q prepared=%t stage=execute expected-affected=2 expected-TEXT-rows=%#v", q, prepared, tc.want)
				run(referenceExec("DROP TABLE dst", 0))
			})
		}
	}
	for _, source := range []string{"bpchar", "varchar"} {
		for _, prepared := range []bool{false, true} {
			t.Run("held/"+source+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				run(referenceExec("CREATE TABLE dst(v TEXT)", 0))
				q := "INSERT INTO dst VALUES(CAST($1 AS text)),(CAST($2 AS " + source + ")) ORDER BY 1"
				frames := [][]any{{"b  ", "a "}, {nil, "c "}}
				if prepared {
					backends := []*sql.DB{p.postgres}
					if !postgresOnly {
						backends = append(backends, p.memory)
					}
					for _, db := range backends {
						stmt, err := db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(q, "prepare", err)
						}
						for _, args := range frames {
							result, err := stmt.ExecContext(p.ctx, args...)
							if err != nil {
								stmt.Close()
								t.Fatal(q, "prepared-execute", args, err)
							}
							count, err := result.RowsAffected()
							if err != nil || count != 2 {
								stmt.Close()
								t.Fatal(count, err)
							}
							t.Logf("query=%q stage=prepared-execute held-args=%#v affected=%d", q, args, count)
						}
						stmt.Close()
					}
				} else {
					for _, args := range frames {
						run(referenceExec(q, 2, args...))
					}
				}
				want := [][]any{{"a "}, {"b  "}, {"c "}, {nil}}
				if source == "bpchar" {
					want = [][]any{{"a"}, {"b  "}, {"c"}, {nil}}
				}
				run(referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"TEXT"}, want))
				run(referenceExec("DROP TABLE dst", 0))
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("atomic/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
			run(referenceExec("CREATE TABLE dst(v VARCHAR(2))", 0))
			run(referenceExec("INSERT INTO dst VALUES('s')", 1))
			q := "INSERT INTO dst VALUES(CAST('aa' AS text)),(CAST('zzx' AS varchar)) ORDER BY 1"
			d := referenceDiagnostic{Code: "22001", Message: "value too long for type character varying(2)"}
			if postgresOnly {
				characterOracleExec(t, p, q, prepared, false, d)
			} else {
				s := referenceImplicitError(q, d)
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
			run(referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"VARCHAR"}, [][]any{{"s"}}))
			run(referenceExec("DROP TABLE dst", 0))
		})
	}
}

// This entrypoint executes SQL on configured pinned PostgreSQL only.
func TestReferenceCharacterSourceValuesOracle(t *testing.T) { characterSourceValuesWitnesses(t, true) }
func TestReferenceCharacterSourceValues(t *testing.T)       { characterSourceValuesWitnesses(t, false) }

// Root's independent pinned18 capture confirmed all direct/prepared witnesses.
// Read unlimited BPCHAR, not TEXT: TEXT readback would conceal extra pads.
func characterSourceValuesCrossTypmodWitnesses(t *testing.T, postgresOnly bool) {
	p := newReferencePair(t)
	run := func(s referenceStep) {
		t.Helper()
		if postgresOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	for _, tc := range []struct {
		first, second string
		want          [][]any
	}{
		{"CAST('b' AS char(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "CAST('a' AS char(2))", [][]any{{"a "}, {"b "}}},
		{"CAST('b' AS varchar(2))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(3))", "CAST('a' AS varchar(2))", [][]any{{"a"}, {"b  "}}},
		{"CAST('a' AS varchar(2))", "CAST('b' AS char(3))", [][]any{{"a"}, {"b"}}},
		{"CAST('b' AS char(2))", "NULL", [][]any{{"b "}, {nil}}},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.first+"/"+tc.second+"/prepared="+strconv.FormatBool(prepared), func(t *testing.T) {
				run(referenceExec("CREATE TABLE dst(v BPCHAR)", 0))
				q := "INSERT INTO dst VALUES(" + tc.first + "),(" + tc.second + ") ORDER BY 1"
				s := referenceExec(q, 2)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
				run(referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"BPCHAR"}, tc.want))
				if !postgresOnly {
					referenceCharacterMetadata(t, p, p.memory, "SELECT v FROM dst", prepared, true, []string{"v"}, []string{"BPCHAR"}, []int64{-1}, []referenceCharacterNullability{{true, true}})
				}
				referenceCharacterMetadata(t, p, p.postgres, "SELECT v FROM dst", prepared, false, []string{"v"}, []string{"BPCHAR"}, []int64{-1}, nil)
				t.Logf("query=%q prepared=%t stage=execute expected-affected=2 expected-physical-BPCHAR-rows=%#v", q, prepared, tc.want)
				run(referenceExec("DROP TABLE dst", 0))
			})
		}
	}
}

// This entrypoint executes SQL only against the configured pinned PostgreSQL.
func TestReferenceCharacterSourceValuesCrossTypmodOracle(t *testing.T) {
	characterSourceValuesCrossTypmodWitnesses(t, true)
}
func TestReferenceCharacterSourceValuesCrossTypmod(t *testing.T) {
	characterSourceValuesCrossTypmodWitnesses(t, false)
}
