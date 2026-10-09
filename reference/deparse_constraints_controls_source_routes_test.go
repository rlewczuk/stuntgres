package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsRenameControlsSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					previous := s.checkMetadata
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						deparseReviewNullableColumns(t, b.pg, rows, "A renamed")
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			run(referenceExec("CREATE TABLE t(a INT,b BIGINT DEFAULT 7,CONSTRAINT ck CHECK(a::bigint>0))", 0))
			conn := b.db
			b.db.SetMaxOpenConns(1)
			id := deparseSourceDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'")
			held, err := conn.Prepare("SELECT pg_get_constraintdef($1,true)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(referenceExec(`ALTER TABLE t RENAME a TO "A renamed"`, 0))
			if deparseSourceDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='ck'") != id {
				t.Fatal("rename changed identity")
			}
			var got string
			if err := held.QueryRow(id).Scan(&got); err != nil || got != `CHECK ("A renamed"::bigint > 0)` {
				t.Fatal(got, err)
			}
			for _, pretty := range []bool{false, true} {
				var suffix string
				if err := conn.QueryRow("SELECT pg_get_constraintdef($1,$2)", id, pretty).Scan(&suffix); err != nil {
					t.Fatal(err)
				}
				name := fmt.Sprintf("copy_%t", pretty)
				run(referenceExec(`CREATE TABLE `+name+`("A renamed" INT,CONSTRAINT copied `+suffix+`)`, 0))
				run(referencePrepared(referenceExec("INSERT INTO "+name+" VALUES(1),(NULL)", 2)))
				run(referenceImplicitError("INSERT INTO "+name+" VALUES(1),(0),(1)", referenceCheckViolation(name, "copied", "0")))
				run(referenceImplicitError("UPDATE "+name+` SET "A renamed"=0`, referenceCheckViolation(name, "copied", "0")))
				run(referencePrepared(referenceImplicitError("UPDATE "+name+` SET "A renamed"=$1`, referenceCheckViolation(name, "copied", "-1"), -1)))
				run(referenceSelect(`SELECT "A renamed" FROM `+name+` ORDER BY "A renamed" NULLS FIRST`, []string{"A renamed"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
			}
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT pg_get_constraintdef($1)", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{nil}}, id))
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				run(referencePrepared(referenceSelect("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{`CHECK ("A renamed"::bigint > 0)`}})))
				run(referenceSelect("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"7"}}))
				for _, script := range []string{"CREATE TABLE bad(a INT CHECK(a))", "CREATE TABLE bad(a INT CONSTRAINT c CHECK(a>0));INSERT INTO bad VALUES(0)"} {
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
						t.Fatal("bad restore succeeded")
					}
					run(referencePrepared(referenceSelect("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{`CHECK ("A renamed"::bigint > 0)`}})))
				}
			}
		})
	}
}

func TestReferenceDeparseConstraintsCancellationAndRollbackSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn := b.db
			b.db.SetMaxOpenConns(1)
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			run := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT DEFAULT 7,CONSTRAINT c CHECK(a>0))", 0))
			id := deparseSourceDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef")
			run(referenceExec("BEGIN", 0))
			if b.pg {
				run(referenceExec("ALTER TABLE t ALTER a SET DEFAULT 3", 0))
			} else {
				run(referenceImplicitError("ALTER TABLE t ALTER a SET DEFAULT 3", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
			}
			run(referenceExec("ROLLBACK", 0))
			if deparseSourceDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef") != id {
				t.Fatal("rollback lost original identity")
			}
			run(referenceSelect("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"7"}}))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, nil))
			run(referenceExec("ALTER TABLE t ALTER a SET DEFAULT 3", 0))
			if deparseSourceDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef") == id {
				t.Fatal("default replacement retained old identity")
			}
			run(referenceSelect("SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"3"}}))
			if b.pg {
				// A precanceled fast pq query can expose a cursor before cancellation.
				// Observe server admission before canceling, preserving this owner.
				cancelConn, err := conn.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceSessionCancel(t, p.ctx, cancelConn, false)
				if err := cancelConn.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				rows, err := conn.QueryContext(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint")
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query exposed cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			run(referencePrepared(referenceSelect("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (a > 0)"}})))
		})
	}
}

func TestReferenceDeparseConstraintsConditionalReplaySourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					previous := s.checkMetadata
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			for _, tc := range deparseReviewConditionalCases {
				if tc.name != "null_arm" {
					continue
				}
				run(referenceExec("CREATE TABLE original(id INT,a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
				for mode, expression := range []string{tc.compact, tc.pretty} {
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,a INT,b INT,flag BOOL,CONSTRAINT c%d CHECK(%s))", mode, mode, expression), 0))
				}
				for _, name := range []string{"original", "copy0", "copy1"} {
					run(referenceExec("INSERT INTO "+name+" VALUES"+tc.good, int64(len(tc.rows))))
				}
				if !b.pg {
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
				}
				for i, name := range []string{"original", "copy0", "copy1"} {
					constraint := "c"
					if i > 0 {
						constraint = fmt.Sprintf("c%d", i-1)
					}
					step := referenceSelect("SELECT id,a,b,flag FROM "+name+" ORDER BY id", []string{"id", "a", "b", "flag"}, []string{"INT4", "INT4", "INT4", "BOOL"}, tc.rows)
					run(step)
					run(referencePrepared(referenceImplicitError("UPDATE "+name+" SET a=0,flag=true WHERE id=1", referenceCheckViolation(name, constraint, "1, 0, 1, t"))))
					run(step)
				}
				for mode, expression := range []string{tc.compact, tc.pretty} {
					step := referenceSelect(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1), []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{expression}})
					run(step)
					run(referencePrepared(step))
				}
			}
		})
	}
}
