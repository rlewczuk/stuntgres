//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteBoundary(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db *sql.DB
		record *referenceRecorder
	}{{"Native", p.memory, p.memoryRecord}, {"Postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			c, err := backend.db.Conn(ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			b, err := backend.db.Conn(ctx)
			if err != nil { t.Fatal(err) }
			defer b.Close()
			aSchema := fmt.Sprintf("delete_boundary_%d", time.Now().UnixNano())
			bSchema := aSchema + "_b"
			defer func() {
				_, _ = c.ExecContext(p.ctx, "ROLLBACK")
				for _, schema := range []string{aSchema, bSchema} {
					if _, err := c.ExecContext(p.ctx, "DROP SCHEMA " + schema + " CASCADE"); err != nil { t.Error(err) }
				}
			}()
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, ctx, c, backend.record, s) }
			exec := func(q string, n int64) { run(referenceExec(q, n)) }
			rows := func(q string, columns, types []string, want [][]any) { run(referenceSelect(q, columns, types, want)) }
			state := func(name string, n int64, called bool) {
				rows("SELECT last_value,is_called FROM " + name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}})
			}
			exec("CREATE SCHEMA " + aSchema, 0)
			exec("CREATE SCHEMA " + bSchema, 0)
			exec("SET search_path TO " + aSchema, 0)
			for _, q := range []string{"CREATE SEQUENCE s START 31 CACHE 1", "CREATE SEQUENCE nested START 47 CACHE 1", "CREATE TABLE empty_t(id INT)", "CREATE TABLE t(id INT)"} { exec(q, 0) }
			exec("INSERT INTO t VALUES(1),(1),(2)", 3)
			bad := controlsReferenceDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`)
			missing := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			missing.Context = "unnamed portal parameter $1 = '...'"
			for _, prepared := range []bool{false, true} {
				for _, target := range []string{"empty_t", "t"} {
					for _, tc := range []struct { q string; diag referenceDiagnostic }{
						{"DELETE FROM " + target + " WHERE setval('s',$1::text::bigint)>0", bad},
						{"DELETE FROM " + target + " WHERE setval($1,nextval('nested'))>0", missing},
					} {
						arg := "bad"
						if tc.diag.Code == "42P01" { arg = "missing" }
						run(referenceStep{query: tc.q, args: []any{arg}, prepared: prepared, state: tc.diag.Code, diagnostic: &tc.diag, errorStage: "execute"})
						rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
						rows("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, nil)
						for _, seq := range []struct { name string; start int64 }{{"s",31},{"nested",47}} {
							state(seq.name, seq.start, false)
							want := controlsReferenceDiagnostic("55000", `currval of sequence "` + seq.name + `" is not yet defined in this session`)
							run(referenceStep{query: "SELECT currval('" + seq.name + "')", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
						}
						want := controlsReferenceDiagnostic("55000", "lastval is not yet defined in this session")
						run(referenceStep{query: "SELECT lastval()", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
					}
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"DELETE FROM empty_t WHERE nextval('s')>0", "DELETE FROM t WHERE false AND nextval(NULL::regclass)>0"} {
					exec("BEGIN", 0)
					run(referenceStep{query: q, prepared: prepared})
					peer, stop := context.WithTimeout(ctx, 2*time.Second)
					r, err := b.ExecContext(peer, "ALTER SEQUENCE " + aSchema + ".s RESTART 80")
					stop()
					if err != nil { t.Fatal("undemanded DELETE blocked peer RESTART while initiating TX remains open", err) }
					if n, err := r.RowsAffected(); err != nil || n != 0 { t.Fatal(n, err) }
					exec("COMMIT", 0)
					state("s", 80, false)
					want := controlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
					run(referenceStep{query: "SELECT currval('s')", selectRows: true, state: want.Code, diagnostic: &want, errorStage: "execute"})
				}
			}
			rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
			// Assert the established backend distinction independently, not
			// parity: native evaluates both logical children, PG folds FALSE.
			exec("CREATE SEQUENCE decisive START 101 CACHE 1",0)
			for i,prepared := range []bool{false,true} {
				exec("BEGIN",0)
				run(referenceStep{query:"DELETE FROM t WHERE false AND nextval('decisive')>0",prepared:prepared})
				if backend.name == "Native" {
					want := int64(103+i*3)
					state("decisive",want,true)
					rows("SELECT currval('decisive')",[]string{"currval"},[]string{"INT8"},[][]any{{want}})
					rows("SELECT lastval()",[]string{"lastval"},[]string{"INT8"},[][]any{{want}})
				} else {
					state("decisive",101,false)
					want := controlsReferenceDiagnostic("55000",`currval of sequence "decisive" is not yet defined in this session`)
					// A diagnostic aborts an explicit PG transaction, so
					// inspect session memory only after ending the DELETE TX.
					exec("ROLLBACK",0)
					run(referenceStep{query:"SELECT currval('decisive')",selectRows:true,state:want.Code,diagnostic:&want,errorStage:"execute"})
				}
				if backend.name == "Native" { exec("ROLLBACK",0) }
				rows("SELECT id FROM t ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(2)}})
			}
			for _, prepared := range []bool{false, true} {
				exec("CREATE SEQUENCE bounded MINVALUE 1 MAXVALUE 2 CACHE 1", 0)
				want := controlsReferenceDiagnostic("2200H", `nextval: reached maximum value of sequence "bounded" (2)`)
				run(referenceStep{query: "DELETE FROM t WHERE nextval('bounded')>0", prepared: prepared, state: want.Code, diagnostic: &want, errorStage: "execute"})
				rows("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
				state("bounded", 2, true)
				rows("SELECT currval('bounded')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}})
				rows("SELECT lastval()", []string{"lastval"}, []string{"INT8"}, [][]any{{int64(2)}})
				exec("DROP SEQUENCE bounded", 0)
			}
			// Same handle, complete two-literal source group, and imported parameter.
			for _, q := range []string{"CREATE SEQUENCE " + bSchema + ".x START 10 CACHE 1", "CREATE SEQUENCE " + bSchema + ".y START 20 CACHE 1", "CREATE TABLE " + bSchema + ".held(id INT)", "SET search_path TO " + aSchema + "," + bSchema} { exec(q,0) }
			exec("INSERT INTO " + bSchema + ".held VALUES(7)",1)
			q := "DELETE FROM " + bSchema + ".held WHERE setval('x',$1::bigint)>0 AND nextval('y')>0"
			s, err := c.PrepareContext(ctx,q)
			if err != nil { t.Fatal("analysis-only DELETE Prepare",err) }
			defer s.Close()
			state(bSchema + ".x",10,false)
			state(bSchema + ".y",20,false)
			baseline, baselineErr := s.ExecContext(ctx,int64(10))
			if baselineErr != nil { t.Fatal(baselineErr) }
			if count,err := baseline.RowsAffected(); err != nil || count != 1 { t.Fatal(count,err) }
			rows("SELECT id FROM " + bSchema + ".held",[]string{"id"},[]string{"INT4"},nil)
			exec("INSERT INTO " + bSchema + ".held VALUES(7)",1)
			exec("CREATE SEQUENCE " + aSchema + ".x START 100 CACHE 1",0)
			exec("ALTER SEQUENCE " + bSchema + ".y RENAME TO identity",0)
			failed := func() {
				_, err := s.ExecContext(ctx,int64(150))
				want := controlsReferenceDiagnostic("42P01", `relation "y" does not exist`)
				want.Position = strconv.Itoa(strings.Index(q,"'y'") + 1)
				referenceAssertDiagnostic(t,err,&want)
				rows("SELECT id FROM " + bSchema + ".held", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			failed()
			state(aSchema + ".x",100,false)
			state(bSchema + ".x",10,true)
			state(bSchema + ".identity",20,true)
			rows("SELECT currval('" + bSchema + ".x')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(10)}})
			rows("SELECT currval('" + bSchema + ".identity')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(20)}})
			rows("SELECT lastval()",[]string{"lastval"},[]string{"INT8"},[][]any{{int64(20)}})
			undefinedShadow := controlsReferenceDiagnostic("55000",`currval of sequence "x" is not yet defined in this session`)
			run(referenceStep{query:"SELECT currval('" + aSchema + ".x')",selectRows:true,state:undefinedShadow.Code,diagnostic:&undefinedShadow,errorStage:"execute"})
			exec("CREATE SEQUENCE " + aSchema + ".y START 200 CACHE 1",0)
			deleted := func(n int64) {
				r, err := s.ExecContext(ctx,n)
				if err != nil { t.Fatal(err) }
				if count,err := r.RowsAffected(); err != nil || count != 1 { t.Fatal(count,err) }
				rows("SELECT id FROM " + bSchema + ".held",[]string{"id"},[]string{"INT4"},nil)
			}
			deleted(150)
			exec("INSERT INTO " + bSchema + ".held VALUES(7)",1)
			exec("SET search_path TO " + bSchema,0)
			failed()
			state(aSchema + ".x",150,true)
			state(aSchema + ".y",200,true)
			state(bSchema + ".x",10,true)
			state(bSchema + ".identity",20,true)
			rows("SELECT currval('" + aSchema + ".x')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(150)}})
			rows("SELECT currval('" + aSchema + ".y')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(200)}})
			rows("SELECT lastval()",[]string{"lastval"},[]string{"INT8"},[][]any{{int64(200)}})
			exec("SET search_path TO " + aSchema + "," + bSchema,0)
			deleted(151)
			state(aSchema + ".x",151,true)
			state(aSchema + ".y",201,true)
			exec("SET search_path TO " + aSchema,0)
			for _, prepared := range []bool{false,true} {
				want := controlsReferenceDiagnostic("42809", `cannot change sequence "s"`)
				run(referenceStep{query: "DELETE FROM s WHERE nextval('s')>0",prepared: prepared,state: want.Code,diagnostic: &want,errorStage: "execute"})
				state("s",80,false)
			}
			exec("DELETE FROM t WHERE id=2",1)
			exec("CREATE SEQUENCE producer START 10 CACHE 1",0)
			exec("CREATE TABLE dst(id INT,n BIGINT)",0)
			run(referencePrepared(referenceExec("INSERT INTO dst SELECT id,nextval('producer') FROM t ORDER BY id",2)))
			rows("SELECT id,n FROM dst ORDER BY n",[]string{"id","n"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(10)},{int64(1),int64(11)}})
			rows("SELECT nextval('producer')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(12)}})
		})
	}
}
