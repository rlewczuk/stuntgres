package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func hbaCollect(t *testing.T, rows *sql.Rows, pg bool) [][]any {
	t.Helper()
	defer rows.Close()
	hbaMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		cells, targets := make([]any, 11), make([]any, 11)
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range cells {
			fixture.AssertRawValue(t, pg, hbaTypes[i], value)
			cells[i] = fixture.Normalize(value)
		}
		got = append(got, cells)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func hbaConsume(t *testing.T, rows *sql.Rows, want [][]any, pg bool) {
	t.Helper()
	if got := hbaCollect(t, rows, pg); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
}

func TestReferencePGHBAFileRulesFreshness(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, hbaLocalSet("local all all peer map=A\n", "peer", "map=A"))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			query := hbaSelect + " ORDER BY rule_number"
			sqlConn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer sqlConn.Close()
			fixture.RunStep(t, p.Ctx, sqlConn, b.Recorder, fixture.Exec("PREPARE hba_fresh AS "+query, 0))
			defer sqlConn.ExecContext(p.Ctx, "DEALLOCATE hba_fresh")
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=B\n", "peer", "map=B"))
			want := [][]any{hbaLocal(1, f.Path(b, ""), 1, "peer", "map=B")}
			hbaRead(t, p, b, held, query, want, true)
			hbaRead(t, p, b, sqlConn, "EXECUTE hba_fresh", want, false)
			old, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=C\n", "peer", "map=C"))
			next := [][]any{hbaLocal(1, f.Path(b, ""), 1, "peer", "map=C")}
			hbaRead(t, p, b, b.DB, query, next, false)
			hbaRead(t, p, b, held, query, next, true)
			hbaRead(t, p, b, sqlConn, "EXECUTE hba_fresh", next, false)
			f.Install(t, p.Ctx, b, fixture.HBARuleSet{})
			for _, e := range []fixture.Executor{b.DB, held} {
				hbaRead(t, p, b, e, query, nil, true)
			}
			hbaRead(t, p, b, sqlConn, "EXECUTE hba_fresh", nil, false)
			hbaConsume(t, old, want, b.PG)
			f.Install(t, p.Ctx, b, hbaLocalSet("local all all peer map=C\n", "peer", "map=C"))
			hbaRead(t, p, b, held, query, next, true)
			if !b.PG {
				t.Run("native-cancelled-admission", func(t *testing.T) {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := b.DB.QueryContext(ctx, query)
					if rows != nil {
						rows.Close()
						t.Fatal("cancelled query exposed cursor")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					hbaRead(t, p, b, held, query, next, true)
				})
				hbaMutableLifecycle(t, p)
				hbaConcurrentBatches(t, p)
			}
		})
	}
}

func TestReferencePGHBAFileRulesReadOnly(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, hbaLocalSet("local all all trust\n", "trust"))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE hba_sentinel(n integer)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO hba_sentinel VALUES(42)", 1))
			for _, tc := range []struct{ query, verb, message, phrase string }{
				{"INSERT INTO pg_catalog.pg_hba_file_rules DEFAULT VALUES", "INSERT", `cannot insert into view "pg_hba_file_rules"`, "inserting into"},
				{"UPDATE pg_catalog.pg_hba_file_rules SET auth_method='trust'", "UPDATE", `cannot update view "pg_hba_file_rules"`, "updating"},
				{"DELETE FROM pg_catalog.pg_hba_file_rules", "DELETE", `cannot delete from view "pg_hba_file_rules"`, "deleting from"},
				{"TRUNCATE pg_catalog.pg_hba_file_rules", "TRUNCATE", `"pg_hba_file_rules" is not a table`, ""},
			} {
				want := fixture.Diagnostic{Code: "55000", Message: tc.message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.phrase + " the view, provide an INSTEAD OF " + tc.verb + " trigger or an unconditional ON " + tc.verb + " DO INSTEAD rule."}
				if tc.verb == "TRUNCATE" {
					want = fixture.Diagnostic{Code: "42809", Message: tc.message}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.verb != "TRUNCATE" {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n FROM hba_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
					hbaRead(t, p, b, b.DB, hbaSelect, [][]any{hbaLocal(1, f.Path(b, ""), 1, "trust")}, prepared)
					hbaInitialACL(t, p, b, b.DB, true)
				}
			}
		})
	}
}
