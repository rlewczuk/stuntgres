//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTableCoreNativeControlRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec(`CREATE TABLE "source.dot"("i.dot" INT NOT NULL,b BIGINT,s TEXT,f BOOL)`, 0))
	p.step(t, referenceExec(`INSERT INTO "source.dot" VALUES(1,2,'source',FALSE)`, 1))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE "clone.dot"(LIKE "source.dot")`, 0)))
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "clone.dot" VALUES($1,$2,$3,$4)`, 1, 7, int64(9223372036854775807), "O'Brien\n\\tail", true)))
	p.step(t, referenceExec(`INSERT INTO "clone.dot" VALUES(8,NULL,NULL,NULL)`, 1))
	p.step(t, referenceExec(`CREATE TABLE z()`, 0))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE zl(LIKE z)`, 0)))
	p.step(t, referenceExec(`DROP TABLE "source.dot" RESTRICT`, 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "LIKE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, `DROP TABLE "clone.dot",z,zl`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL rejected native expanded dump", err)
	}
	expected := referenceSelect(`SELECT * FROM "clone.dot" ORDER BY "i.dot"`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien\n\\tail", true}, {int64(8), nil, nil, nil}})
	p.step(t, expected)
	p.step(t, referencePrepared(expected))
	for _, z := range []string{"z", "zl"} {
		p.step(t, referenceSelect("SELECT * FROM "+z, []string{}, []string{}, nil))
		p.step(t, referencePrepared(referenceSelect("SELECT * FROM "+z, []string{}, []string{}, nil)))
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	p.step(t, referenceImplicitError(`INSERT INTO "clone.dot" VALUES(NULL,NULL,NULL,NULL)`, referenceDiagnostic{Code: "23502", Message: `null value in column "i.dot" of relation "clone.dot" violates not-null constraint`, Detail: "Failing row contains (null, null, null, null).", Schema: "public", Table: "clone.dot", Column: "i.dot"}))
	for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(id INT,id TEXT)", "CREATE TABLE replacement(id no_such_type)", "CREATE TABLE replacement(LIKE z INCLUDING ALL)", "DROP TABLE z"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted", script)
		}
		p.step(t, expected)
		redump.Reset()
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("failed restore changed catalog", err)
		}
	}
	p.step(t, referencePrepared(referenceExec(`ALTER TABLE "clone.dot" DROP CONSTRAINT "source.dot_i.dot_not_null"`, 0)))
	p.step(t, expected)
	p.step(t, referencePrepared(referenceExec(`INSERT INTO "clone.dot" VALUES(NULL,99,'released',FALSE)`, 1)))
	p.step(t, referenceSelect(`SELECT * FROM "clone.dot" WHERE b=99`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{nil, int64(99), "released", false}}))
	absent := referenceImplicitError(`SELECT * FROM "source.dot"`, referenceDiagnostic{Code: "42P01", Message: `relation "source.dot" does not exist`})
	pgAbsent := *absent.diagnostic
	pgAbsent.Position = "15"
	absent.postgresDiagnostic = &pgAbsent
	p.step(t, absent)
}

func TestReferenceTableCoreAuthorizationAndOwnershipCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner, reader := fmt.Sprintf("table_owner_%v", prepared), fmt.Sprintf("table_reader_%v", prepared)
			roleReferenceCleanup(t, []string{owner, reader})
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE USER "+owner, 0))
			p.step(t, referenceExec("CREATE USER "+reader, 0))
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC"); err != nil {
				t.Fatal(err)
			}
			for _, db := range []*sql.DB{p.memory, p.postgres} {
				conn, err := db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer conn.Close()
					record := &referenceRecorder{}
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, conn, record, s) }
					run(referenceExec("SET SESSION AUTHORIZATION "+owner, 0))
					run(referenceExec("CREATE TABLE source(i INT NOT NULL)", 0))
					run(referenceExec("INSERT INTO source VALUES(7)", 1))
					run(referenceExec("SET SESSION AUTHORIZATION "+reader, 0))
					denied := referenceImplicitError("CREATE TABLE denied(LIKE source)", referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
					denied.errorStage = "execute"
					run(denied)
					denied = referenceImplicitError("DROP TABLE source", referenceDiagnostic{Code: "42501", Message: "must be owner of table source"})
					denied.errorStage = "execute"
					run(denied)
					run(referenceExec("RESET SESSION AUTHORIZATION", 0))
					blocked := referenceImplicitError("DROP ROLE "+owner, referenceDiagnostic{Code: "2BP01", Message: fmt.Sprintf("role %q cannot be dropped because some objects depend on it", owner), Detail: "owner of table source"})
					blocked.errorStage = "execute"
					run(blocked)
					run(referenceSelect("SELECT * FROM source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceExec("DROP TABLE source CASCADE", 0))
					run(referenceExec("DROP ROLE "+owner, 0))
					run(referenceExec("DROP ROLE "+reader, 0))
				}()
			}
		})
	}
}

func TestReferenceTableCoreHeldCursorAndPreparedRebinding(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name     string
		db       *sql.DB
		postgres bool
	}{{"native", p.memory, false}, {"postgres", p.postgres, true}} {
		t.Run(backend.name, func(t *testing.T) {
			record := &referenceRecorder{}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			referenceRunStep(t, ctx, backend.db, record, referenceExec("CREATE TABLE held(id INT)", 0))
			referenceRunStep(t, ctx, backend.db, record, referenceExec("INSERT INTO held VALUES(11),(22)", 2))
			stmt, err := backend.db.PrepareContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := backend.db.QueryContext(ctx, "SELECT id FROM held ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			referenceRunStep(t, ctx, backend.db, record, referenceExec("DROP TABLE held RESTRICT", 0))
			failed, err := stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("absent prepared query exposed rows")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "held" does not exist`, Position: "16"}
			referenceAssertDiagnostic(t, err, &want)
			referenceRunStep(t, ctx, backend.db, record, referenceExec("CREATE TABLE held(other TEXT)", 0))
			referenceRunStep(t, ctx, backend.db, record, referenceExec("INSERT INTO held VALUES('fresh')", 1))
			failed, err = stmt.QueryContext(ctx)
			if failed != nil {
				failed.Close()
				t.Fatal("incompatible prepared query exposed rows")
			}
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "id" does not exist`, Position: "8"}
			referenceAssertDiagnostic(t, err, &want)
			labels, err := held.Columns()
			if err != nil || !reflect.DeepEqual(labels, []string{"id"}) {
				t.Fatal(labels, err)
			}
			types, err := held.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
				t.Fatal(types, err)
			}
			var values []int64
			for held.Next() {
				var value int64
				if err = held.Scan(&value); err != nil {
					t.Fatal(err)
				}
				values = append(values, value)
			}
			if err = held.Err(); err != nil || !reflect.DeepEqual(values, []int64{11, 22}) {
				t.Fatal(values, err)
			}
			held.Close()
			referenceRunStep(t, ctx, backend.db, record, referenceSelect("SELECT * FROM held", []string{"other"}, []string{"TEXT"}, [][]any{{"fresh"}}))
		})
	}
}
