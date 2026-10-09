package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortTargets(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					p := newReferenceScenario(t)
					route := func(s referenceStep) referenceStep {
						s.nonContext = nonContext
						return s
					}
					p.step(t, route(referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0)))
					p.step(t, route(referenceExec("CREATE TABLE repeats(a INT,b INT,tail TEXT)", 0)))
					for _, step := range []referenceStep{
						referenceExec("INSERT INTO t VALUES (1)", 1),
						referenceExec("INSERT INTO t VALUES ($1,$2)", 1, 2, "20"),
						referenceExec("INSERT INTO t VALUES (3,30,'three')", 1),
						referenceExec("INSERT INTO t VALUES ($1)", 1, "5"),
						referenceExec("INSERT INTO t VALUES ($1)", 1, []byte("6")),
						referenceExec("INSERT INTO t(label,id) VALUES ('reordered',4)", 1),
						referenceExec("INSERT INTO t VALUES (7,70,'full',TRUE)", 1),
						referenceExec("INSERT INTO t VALUES (8),(9)", 2),
						referenceExec("INSERT INTO t(label,id) VALUES ('ten',10),('eleven',11)", 2),
						referenceExec("INSERT INTO repeats VALUES ($1,$1),($2,$2)", 2, 11, 12),
					} {
						step.prepared = prepared
						p.step(t, route(step))
					}
					want := [][]any{
						{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), int64(30), "three", nil},
						{int64(4), nil, "reordered", nil}, {int64(5), nil, nil, nil}, {int64(6), nil, nil, nil},
						{int64(7), int64(70), "full", true}, {int64(8), nil, nil, nil}, {int64(9), nil, nil, nil},
						{int64(10), nil, "ten", nil}, {int64(11), nil, "eleven", nil},
					}
					step := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, want)
					step.prepared = prepared
					for _, backend := range p.backends() {
						step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
							referenceInsertShortMetadata(t, rows, backend.pg, step.types, []bool{false, true, true, true})
						}
						if nonContext && prepared {
							// Original wantTuples uses DB.Query even after prepared writes.
							direct := step
							direct.prepared = false
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, route(direct))
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, route(step))
					}
					p.step(t, route(referencePrepared(referenceSelect("SELECT * FROM t WHERE id = $1", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(2), int64(20), nil, nil}}, 2))))
					p.step(t, route(referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b", "tail"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(11), int64(11), nil}, {int64(12), int64(12), nil}})))
					p.step(t, route(referenceExec(`CREATE TABLE "Odd"("Id" INT NOT NULL,label TEXT)`, 0)))
					odd := referenceExec(`INSERT INTO "Odd" VALUES ($1)`, 1, "7")
					odd.prepared = prepared
					p.step(t, route(odd))
					p.step(t, route(referenceSelect(`SELECT * FROM "Odd" ORDER BY "Id"`, []string{"Id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), nil}})))
					for _, backend := range p.backends() {
						step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
							referenceInsertShortMetadata(t, rows, backend.pg, step.types, []bool{false, true, true, true})
						}
						for _, s := range []referenceStep{
							referenceImplicitError("INSERT INTO t VALUES ($2)", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}, 10),
							referenceImplicitError("INSERT INTO t VALUES ($1,$1)", referenceDiagnostic{Code: "0A000", Message: "parameter reused with different destination types"}, 10),
						} {
							if backend.pg && s.state == "0A000" {
								s = referenceImplicitError(s.query, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: referencePosition(strings.LastIndex(s.query, "$1") + 1)}, 10)
							}
							s.prepared = prepared
							s.errorStage = "execute"
							if prepared {
								s.errorStage = "prepare"
							}
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, route(s))
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, route(step))
					}
				})
			}
		})
	}
}

func TestReferenceInsertImplicitShortQuotedDiagnostics(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			route := func(s referenceStep) referenceStep {
				s.nonContext = nonContext
				return s
			}
			p.step(t, route(referenceExec(`CREATE TABLE "t""é"("a""b" INT NOT NULL,label TEXT,flag BOOL)`, 0)))
			p.step(t, route(referenceExec(`INSERT INTO "t""é" VALUES(99,'sentinel',TRUE)`, 1)))
			for _, tc := range []struct {
				query, code, message, hint, token string
			}{
				{`INSERT INTO "t""é" VALUES(TRUE)`, "42804", `column "a"b" is of type integer but expression is of type boolean`, "You will need to rewrite or cast the expression.", "TRUE"},
				{`INSERT INTO "t""é"("a""b","a""b") VALUES(1,2)`, "42701", `column "a"b" specified more than once`, "", `"a""b"`},
				{`INSERT INTO "t""é"("m""issing") VALUES(1)`, "42703", `column "m"issing" of relation "t"é" does not exist`, "", `"m""issing"`},
			} {
				position := referencePosition(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, tc.token)]) + 1)
				step := referenceImplicitError(tc.query, referenceDiagnostic{
					Code: tc.code, Message: tc.message, Hint: tc.hint, Position: position,
				})
				step.errorStage = "execute"
				p.step(t, route(step))
				p.step(t, route(referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}})))
				step.errorStage = "prepare"
				p.step(t, route(referencePrepared(step)))
				p.step(t, route(referenceSelect(`SELECT * FROM "t""é" ORDER BY "a""b"`, []string{`a"b`, "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(99), "sentinel", true}})))
			}
		})
	}
}

func TestReferenceInsertImplicitShortDumpRoundTrip(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recordedContext", true: "sourceConsumerRoutes"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			route := func(s referenceStep) referenceStep {
				s.nonContext = nonContext
				return s
			}
			referenceRunStep(t, p.ctx, p.native, p.memoryRecord, route(referenceExec("CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)", 0)))
			referenceRunStep(t, p.ctx, p.native, p.memoryRecord, route(referenceExec("INSERT INTO t VALUES(1),(2)", 2)))
			referenceRunStep(t, p.ctx, p.native, p.memoryRecord, route(referenceExec("INSERT INTO t(label,id) VALUES('reordered',3)", 1)))
			want := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil}})
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, route(want))
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatalf("execute native dump on PostgreSQL: %v\n%s", err, dump.String())
				}
			}
			p.step(t, route(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{
				{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "reordered", nil},
			})))
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			p.step(t, route(want))
			var second bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &second); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), second.Bytes()) {
				t.Fatalf("short-row dump changed:\n%s\n%s", dump.String(), second.String())
			}
			err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE staged(id INT,label TEXT); INSERT INTO staged VALUES(7); CREATE TABLE required(id INT,flag BOOL NOT NULL); INSERT INTO required VALUES(8);"))
			diagnostic := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "flag" of relation "required" violates not-null constraint`, Detail: "Failing row contains (8, null).", Schema: "public", Table: "required", Column: "flag"}
			referenceAssertDiagnostic(t, err, &diagnostic)
			p.step(t, route(want))
			var rows *sql.Rows
			if nonContext {
				rows, err = p.native.Query("SELECT * FROM staged")
			} else {
				rows, err = p.native.QueryContext(p.ctx, "SELECT * FROM staged")
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
			if err := stuntgres.Dump(p.ctx, p.native, &preserved); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), preserved.Bytes()) {
				t.Fatal("failed short staged restore changed catalog")
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES(8);")); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, route(referenceSelect("SELECT * FROM restored", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), nil}})))
		})
	}
}
