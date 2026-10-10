package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRegtypeRecursiveMetadata(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		for _, q := range []string{
			"CREATE DOMAIN regtype_a.identifier AS regtype",
			"CREATE DOMAIN regtype_a.identifier_array AS regtype[]",
			"CREATE TABLE regtype_a.recursive_subject(id integer NOT NULL,d regtype_a.identifier,da regtype_a.identifier[],ad regtype_a.identifier_array)",
		} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.recursive_subject VALUES(1,'regtype_a.same','{integer,regtype_b.same,NULL}','{integer,regtype_a.same,NULL}'),(2,NULL,NULL,NULL)", 2))
		for _, predicate := range []string{"id=1", "id=2", "id=99"} {
			query := "SELECT d,da,ad FROM regtype_a.recursive_subject WHERE " + predicate
			for _, prepared := range []bool{false, true} {
				var stmt *sql.Stmt
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, err = conn.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = conn.QueryContext(p.ctx, query)
				}
				if err != nil {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatal(err)
				}
				names := []string{"REGTYPE", "_IDENTIFIER", "_REGTYPE"}
				if backend.pg {
					names[1] = ""
				}
				regtypeCursorMetadata(t, rows, backend.pg, []string{"d", "da", "ad"}, names, []bool{true, true, true})
				count := 0
				for rows.Next() {
					var d, da, ad any
					if err := rows.Scan(&d, &da, &ad); err != nil {
						t.Fatal(err)
					}
					for _, value := range []any{d, da, ad} {
						regtypeRaw(t, backend.pg, value)
					}
					if predicate == "id=2" {
						if d != nil || da != nil || ad != nil {
							t.Fatal("NULL recursive transport", d, da, ad)
						}
					} else if referenceNormalize(d) != "same" || referenceNormalize(da) != "{integer,regtype_b.same,NULL}" || referenceNormalize(ad) != "{integer,same,NULL}" {
						t.Fatal("recursive regtype values", d, da, ad)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				want := 1
				if predicate == "id=99" {
					want = 0
				}
				if count != want {
					t.Fatal("recursive row count", count, want)
				}
			}
		}
		// Multiple dimensions and nonstandard bounds preserve type identity; late
		// domain-array input failure is eager and does not publish earlier leaves.
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.recursive_subject(id,da) VALUES(3,$1)", 1, "[0:1]={integer,regtype_a.same}"))
		rows, err := conn.QueryContext(p.ctx, "SELECT da FROM regtype_a.recursive_subject WHERE id=3")
		if err != nil {
			t.Fatal(err)
		}
		arrayType := "_IDENTIFIER"
		if backend.pg {
			arrayType = ""
		}
		regtypeCursorMetadata(t, rows, backend.pg, []string{"da"}, []string{arrayType}, []bool{true})
		regtypeAssertRows(t, rows, [][]any{{"[0:1]={integer,same}"}}, backend.pg)
		result, err := conn.ExecContext(p.ctx, "INSERT INTO regtype_a.recursive_subject(id,da) VALUES(4,$1)", "{integer,missing_regtype_name}")
		if result != nil {
			t.Error("failed domain array input returned result")
		}
		regtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, "INSERT INTO regtype_a.recursive_subject(id,da) VALUES(4,$1)", "domain-array-frame", "execute")
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT count(*) AS n FROM regtype_a.recursive_subject WHERE id=4", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
