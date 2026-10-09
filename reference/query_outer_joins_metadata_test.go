package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceQueryOuterJoinsMetadata(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE narrow(k INTEGER NOT NULL)", 0), referenceExec("CREATE TABLE wide(k BIGINT NOT NULL)", 0),
			referenceExec("CREATE TABLE char_a(k VARCHAR(3))", 0), referenceExec("CREATE TABLE char_b(k VARCHAR(5))", 0), referenceExec("CREATE TABLE char_c(k VARCHAR(3))", 0),
			referenceExec("INSERT INTO narrow VALUES(1),(2)", 2), referenceExec("INSERT INTO wide VALUES(1),(3)", 2),
			referenceExec("INSERT INTO char_a VALUES('x'),('a')", 2), referenceExec("INSERT INTO char_b VALUES('x'),('b')", 2), referenceExec("INSERT INTO char_c VALUES('x'),('c')", 2),
		} {
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			keys := [][]any{{int64(1), int64(1), int64(1)}}
			if kind != "RIGHT" {
				keys = append(keys, []any{int64(2), int64(2), nil})
			}
			if kind != "LEFT" {
				keys = append(keys, []any{int64(3), nil, int64(3)})
			}
			outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT k,narrow.k,wide.k FROM narrow "+kind+" JOIN wide USING(k)", []string{"k", "k", "k"}, []string{"INT8", "INT4", "INT8"}, keys))
			for _, operands := range []string{"narrow " + kind + " JOIN wide", "wide " + kind + " JOIN narrow", "narrow a " + kind + " JOIN narrow b"} {
				for _, prepared := range []bool{false, true} {
					query := "SELECT * FROM " + operands + " USING(k)"
					outerJoinsAssert(t, p, conn, recorder, query, prepared)
					outerJoinsAssertMetadata(t, p, conn, recorder, query, prepared)
				}
			}
			for _, right := range []string{"char_b", "char_c"} {
				rows := [][]any{{"x", "x", "x"}}
				if kind != "RIGHT" {
					rows = append(rows, []any{"a", "a", nil})
				}
				if kind != "LEFT" {
					value := "b"
					if right == "char_c" {
						value = "c"
					}
					rows = append(rows, []any{value, nil, value})
				}
				query := "SELECT k,char_a.k," + right + ".k FROM char_a " + kind + " JOIN " + right + " USING(k)"
				outerJoinsCases(t, p, conn, recorder, referenceSelect(query, []string{"k", "k", "k"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, rows))
				for _, prepared := range []bool{false, true} {
					outerJoinsAssert(t, p, conn, recorder, query, prepared)
					outerJoinsAssertMetadata(t, p, conn, recorder, query, prepared)
				}
			}
		}
	})
}

func TestReferenceQueryOuterJoinsViews(t *testing.T) {
	outerJoinsScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, form := range []string{"ON", "USING", "NATURAL"} {
				name := "v_" + kind + "_" + form
				from := "a " + kind + " JOIN b USING(k)"
				if form == "ON" {
					from = "a " + kind + " JOIN b ON a.k=b.k"
				}
				if form == "NATURAL" {
					from = "a NATURAL " + kind + " JOIN b"
				}
				projection := "*"
				if form == "ON" {
					projection = "a.k AS ak,a.l,b.k AS bk,b.r"
				}
				referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE VIEW "+name+" AS SELECT "+projection+" FROM "+from, 0))
				if form == "ON" {
					outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT * FROM "+name, []string{"ak", "l", "bk", "r"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, outerJoinsOnRows(kind)))
				} else {
					outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT * FROM "+name, []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows(kind)))
				}
				var definition string
				if err := conn.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", strings.ToLower(name)).Scan(&definition); err != nil {
					t.Fatal(err)
				}
				outerJoinsAssertViewDefinition(t, name, definition)
			}
		}
		naturalQuery := "SELECT * FROM a NATURAL LEFT JOIN b"
		held, err := conn.PrepareContext(p.ctx, naturalQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldExecutor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: naturalQuery}
		initial := referenceSelect(naturalQuery, []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows("LEFT"))
		initial.bag = true
		referenceRunStep(t, p.ctx, heldExecutor, recorder, initial)
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE disjoint(z BOOLEAN)", 0),
			referenceExec("CREATE VIEW no_keys AS SELECT * FROM a NATURAL LEFT JOIN disjoint", 0),
			referenceExec("CREATE VIEW nested AS SELECT * FROM v_FULL_NATURAL", 0),
			referenceExec("ALTER TABLE b RENAME COLUMN r TO l", 0),
		} {
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		fresh := referenceImplicitError(naturalQuery, referenceDiagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		fresh.selectRows, fresh.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, heldExecutor, recorder, fresh)
		outerJoinsCases(t, p, conn, recorder,
			referenceSelect("SELECT * FROM nested", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows("FULL")),
			referenceSelect("SELECT * FROM a NATURAL LEFT JOIN b", []string{"k", "l"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), "b"}, {nil, "n"}}),
			referenceSelect("SELECT * FROM no_keys", []string{"k", "l", "z"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "a", nil}, {int64(1), "a", nil}, {int64(2), "b", nil}, {nil, "n", nil}}))
		for _, s := range []referenceStep{referenceExec("ALTER TABLE a RENAME COLUMN k TO key", 0), referenceExec("ALTER TABLE b RENAME COLUMN k TO key", 0), referenceExec("ALTER TABLE a RENAME TO renamed_a", 0)} {
			referenceRunStep(t, p.ctx, conn, recorder, s)
		}
		outerJoinsCases(t, p, conn, recorder, referenceSelect("SELECT * FROM nested", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, outerJoinsMergedRows("FULL")))
		for _, name := range []string{"no_keys", "nested", "v_full_natural"} {
			var definition string
			if err := conn.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			outerJoinsAssertViewDefinition(t, name, definition)
		}
	})
}
