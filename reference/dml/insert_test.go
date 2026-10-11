package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortTargets(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					p := fixture.NewScenario(t)
					route := func(s fixture.Step) fixture.Step {
						s.NonContext = nonContext
						return s
					}
					p.Step(t, route(fixture.Exec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0)))
					p.Step(t, route(fixture.Exec("CREATE TABLE repeats(a INT,b INT,tail TEXT)", 0)))
					for _, step := range []fixture.Step{
						fixture.Exec("INSERT INTO t VALUES (1)", 1),
						fixture.Exec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20"),
						fixture.Exec("INSERT INTO t VALUES (3,30,'three')", 1),
						fixture.Exec("INSERT INTO t VALUES ($1)", 1, "5"),
						fixture.Exec("INSERT INTO t VALUES ($1)", 1, []byte("6")),
						fixture.Exec("INSERT INTO t(label,id) VALUES ('reordered',4)", 1),
						fixture.Exec("INSERT INTO t VALUES (7,70,'full',TRUE)", 1),
						fixture.Exec("INSERT INTO t VALUES (8),(9)", 2),
						fixture.Exec("INSERT INTO t(label,id) VALUES ('ten',10),('eleven',11)", 2),
						fixture.Exec("INSERT INTO repeats VALUES ($1,$1),($2,$2)", 2, 11, 12),
					} {
						step.Prepared = prepared
						p.Step(t, route(step))
					}
					want := [][]any{
						{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), int64(30), "three", nil},
						{int64(4), nil, "reordered", nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil},
						{int64(7), int64(70), "full", true}, {int64(8), nil, nil, nil}, {int64(9), nil, nil, nil},
						{int64(10), nil, "ten", nil}, {int64(11), nil, "eleven", nil},
					}
					step := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, want)
					step.Prepared = prepared
					for _, backend := range p.Backends() {
						step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							referenceInsertShortMetadata(t, rows, backend.PG, step.Types, []bool{false, true, true, true})
						}
						if nonContext && prepared {
							// Original wantTuples uses DB.Query even after prepared writes.
							direct := step
							direct.Prepared = false
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, route(direct))
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, route(step))
					}
					p.Step(t, route(fixture.Prepared(fixture.Select("SELECT * FROM t WHERE id = $1", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(2), int64(20), nil, nil}}, 2))))
					p.Step(t, route(fixture.Select("SELECT * FROM repeats ORDER BY a", []string{"a", "b", "tail"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(11), int64(11), nil}, {int64(12), int64(12), nil}})))
					p.Step(t, route(fixture.Exec(`CREATE TABLE "Odd"("Id" INT NOT NULL,label TEXT)`, 0)))
					odd := fixture.Exec(`INSERT INTO "Odd" VALUES ($1)`, 1, "7")
					odd.Prepared = prepared
					p.Step(t, route(odd))
					p.Step(t, route(fixture.Select(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}})))
					for _, backend := range p.Backends() {
						step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							referenceInsertShortMetadata(t, rows, backend.PG, step.Types, []bool{false, true, true, true})
						}
						for _, s := range []fixture.Step{
							fixture.ImplicitError("INSERT INTO t VALUES ($2)", fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}, 10),
							fixture.ImplicitError("INSERT INTO t VALUES ($1,$1)", fixture.Diagnostic{Code: "0A000", Message: "parameter reused with different destination types"}, 10),
						} {
							if backend.PG && s.State == "0A000" {
								s = fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: fixture.Position(strings.LastIndex(s.Query, "$1") + 1)}, 10)
							}
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, route(s))
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, route(step))
					}
				})
			}
		})
	}
}

func TestReferenceInsertImplicitShortQuotedDiagnostics(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			route := func(s fixture.Step) fixture.Step {
				s.NonContext = nonContext
				return s
			}
			p.Step(t, route(fixture.Exec(`CREATE TABLE "t""é"("a""b" INT NOT NULL,label TEXT,flag BOOL)`, 0)))
			p.Step(t, route(fixture.Exec(`INSERT INTO "t""é" VALUES(99,'sentinel',TRUE)`, 1)))
			for _, tc := range []struct {
				query, code, message, hint, token string
			}{
				{`INSERT INTO "t""é" VALUES(TRUE)`, "42804", `column "a"b" is of type integer but expression is of type boolean`, "You will need to rewrite or cast the expression.", "TRUE"},
				{`INSERT INTO "t""é"("a""b","a""b") VALUES(1,2)`, "42701", `column "a"b" specified more than once`, "", `"a""b"`},
				{`INSERT INTO "t""é"("m""issing") VALUES(1)`, "42703", `column "m"issing" of relation "t"é" does not exist`, "", `"m""issing"`},
			} {
				position := fixture.Position(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
				step := fixture.ImplicitError(tc.query, fixture.Diagnostic{
					Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position,
				})
				step.ErrorStage = "execute"
				p.Step(t, route(step))
				p.Step(t, route(fixture.Select(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}})))
				step.ErrorStage = "prepare"
				p.Step(t, route(fixture.Prepared(step)))
				p.Step(t, route(fixture.Select(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}})))
			}
		})
	}
}

func TestReferenceInsertImplicitShortDumpRoundTrip(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			route := func(s fixture.Step) fixture.Step {
				s.NonContext = nonContext
				return s
			}
			fixture.RunStep(t, p.Ctx, p.Native, p.MemoryRecord, route(fixture.Exec("CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)", 0)))
			fixture.RunStep(t, p.Ctx, p.Native, p.MemoryRecord, route(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2)))
			fixture.RunStep(t, p.Ctx, p.Native, p.MemoryRecord, route(fixture.Exec("INSERT INTO t(label,id) VALUES('reordered',3)", 1)))
			want := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil}})
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, route(want))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatalf("execute native dump on PostgreSQL: %v\n%s", err, dump.String())
				}
			}
			p.Step(t, route(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{
				{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil},
			})))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			p.Step(t, route(want))
			var second bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), second.Bytes()) {
				t.Fatalf("short-row dump changed:\n%s\n%s", dump.String(), second.String())
			}
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE staged(id INT,label TEXT); INSERT INTO staged VALUES(7); CREATE TABLE required(id INT,flag BOOL NOT NULL); INSERT INTO required VALUES(8);"))
			diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "flag" of relation "required" violates not-null constraint`, Detail: "Failing row contains (8, null).", Schema: "public", Table: "required", Column: "flag"}
			fixture.AssertDiagnostic(t, err, &diagnostic)
			p.Step(t, route(want))
			var rows *sql.Rows
			if nonContext {
				rows, err = p.Native.Query("SELECT * FROM staged")
			} else {
				rows, err = p.Native.QueryContext(p.Ctx, "SELECT * FROM staged")
			}
			if rows != nil {
				rows.Close()
				t.Fatal("failed Restore published cursor for staged table")
			}
			var missing *stuntgres.Error
			if !errors.As(err, &missing) || missing.Code != "42P01" {
				t.Fatalf("staged table visibility: %v", err)
			}
			var preserved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &preserved); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
				t.Fatal("failed short staged restore changed catalog")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES(8);")); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, route(fixture.Select("SELECT * FROM restored", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), nil}})))
		})
	}
}
