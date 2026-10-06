//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceUpdateCoreDestinationMatrix(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			record := p.memoryRecord
			if i == 1 {
				record = p.postgresRecord
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, db, record, s) }
			run(referenceExec("CREATE TABLE t(id INT,src INT,s TEXT,u UUID,tm TIME(2),ft TIME,ts TIMESTAMP(2),fs TIMESTAMP,n NUMERIC(3,1),fn NUMERIC,nn NUMERIC(2,-3),nw NUMERIC(3,5),b BOOL,r REAL,d DOUBLE PRECISION,sm SMALLINT,bi BIGINT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,7,'old',NULL,NULL,TIME '04:05:06.126',NULL,TIMESTAMP '2024-01-02 03:04:05.126',1.0,1.25,0,0,FALSE,0,2.5,0,0),(2,-7,'old',NULL,NULL,TIME '04:05:06.124',NULL,TIMESTAMP '2024-01-02 03:04:05.124',1.0,-1.25,0,0,FALSE,0,-2.5,0,0)", 2))
			q := "UPDATE t SET s=src,u=$1,tm=ft,ts=fs,n=fn,nn=$2,nw=$3,b=$4,r=d,sm=src,bi=src"
			names := []string{"id", "s", "u", "tm", "ts", "n", "nn", "nw", "b", "r", "sm", "bi"}
			types := []string{"INT4", "TEXT", "UUID", "TIME", "TIMESTAMP", "NUMERIC", "NUMERIC", "NUMERIC", "BOOL", "FLOAT4", "INT2", "INT8"}
			clocks := [2]any{"04:05:06.13", "04:05:06.12"}
			if i == 1 {
				clocks = [2]any{time.Date(0, 1, 1, 4, 5, 6, 130000000, time.UTC), time.Date(0, 1, 1, 4, 5, 6, 120000000, time.UTC)}
			}
			expected := func(uuid, boolean any) [][]any {
				return [][]any{
					{int64(1), "7", uuid, clocks[0], time.Date(2024, 1, 2, 3, 4, 5, 130000000, time.UTC), "1.3", "2000", "0.00999", boolean, float64(2.5), int64(7), int64(7)},
					{int64(2), "-7", uuid, clocks[1], time.Date(2024, 1, 2, 3, 4, 5, 120000000, time.UTC), "-1.3", "2000", "0.00999", boolean, float64(-2.5), int64(-7), int64(-7)},
				}
			}
			check := func(uuid, boolean any) {
				run(referenceSelect("SELECT id,s,u,tm,ts,n,nn,nw,b,r,sm,bi FROM t ORDER BY id", names, types, expected(uuid, boolean)))
			}
			run(referenceExec(q, 2, "{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", "1500", "0.009994", true))
			check(uuidA, true)
			stmt, err := db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{nil, "1500", "0.009994", nil}, {uuidA, "1500", "0.009994", true}} {
				result, err := stmt.ExecContext(p.ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, 2)
				check(args[0], args[3])
			}
			// Parameter typmods are destination execution work, not cached frame sizing.
			overflow := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}
			for _, prepared := range []bool{false, true} {
				step := referenceImplicitError("UPDATE t SET n=$1,s='discard'", overflow, "99.95")
				step.prepared = prepared
				step.errorStage = "execute"
				run(step)
				check(uuidA, true)
				constant := referenceImplicitError("UPDATE t SET n=99.95 WHERE FALSE", overflow)
				constant.prepared = prepared
				constant.errorStage = "execute"
				run(constant)
				check(uuidA, true)
			}
			run(referenceExec("UPDATE t SET fn=99.95 WHERE id=2", 1))
			late, err := db.PrepareContext(p.ctx, "UPDATE t SET n=fn,s='discard'")
			if err != nil {
				t.Fatal(err)
			}
			defer late.Close()
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					_, err = late.ExecContext(p.ctx)
				} else {
					_, err = db.ExecContext(p.ctx, "UPDATE t SET n=fn,s='discard'")
				}
				referenceAssertDiagnostic(t, err, &overflow)
				check(uuidA, true)
			}
			// NULL is typed independently for every codec and sizing provider.
			for _, prepared := range []bool{false, true} {
				step := referenceExec("UPDATE t SET s=$1,u=$2,tm=$3,ts=$4,n=$5,nn=$6,nw=$7,b=$8,r=$9,sm=$10,bi=$11", 2, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				step.prepared = prepared
				run(step)
			}
			run(referenceSelect("SELECT s,u,tm,ts,n,nn,nw,b,r,sm,bi FROM t ORDER BY id", names[1:], types[1:], [][]any{{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}}))
			bad := referenceImplicitError("UPDATE t SET u='bad'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: "16"})
			run(bad)
			bad.prepared = true
			bad.errorStage = "prepare"
			run(bad)
		})
	}
}
