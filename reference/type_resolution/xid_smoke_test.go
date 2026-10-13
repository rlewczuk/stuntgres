package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXIDFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	want := [][]any{{int64(1), "0", "0", "0"}, {int64(2), "4294967295", "18446744073709551615", "4294967295"}, {int64(3), nil, nil, nil}, {int64(4), "4294967295", "18446744073709551615", "4294967295"}, {int64(5), "0", "9223372036854775808", "0"}}
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_smoke(id INT,x XID,y XID8,c CID)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO xid_smoke VALUES(1,'0','0','0'),(2,'4294967295','18446744073709551615','4294967295'),(3,NULL,NULL,NULL)", 3))
		q := "INSERT INTO xid_smoke VALUES($1,$2,$3,$4)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 4, "-1", "-1", "-1"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, "0", "9223372036854775808", "0"))
		xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT id,x,y,c FROM xid_smoke ORDER BY id", []string{"id", "x", "y", "c"}, []string{"INT4", "XID", "XID8", "CID"}, want))
		xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT x=(-1)::int4 AS xe,c='4294967295'::cid AS ce,y>'9223372036854775808'::xid8 AS yg,y::xid AS low FROM xid_smoke WHERE id=2", []string{"xe", "ce", "yg", "low"}, []string{"BOOL", "BOOL", "BOOL", "XID"}, [][]any{{true, true, true, "4294967295"}}))
		for _, q := range []string{"SELECT x<'1'::xid FROM xid_smoke WHERE id=99", "SELECT c<>'1'::cid FROM xid_smoke WHERE id=3"} {
			typ, op := "xid", "<"
			if strings.Contains(q, "c<>") {
				typ, op = "cid", "<>"
			}
			xidReferenceAnalysisError(t, p, b, xidReferenceOperatorError(q, typ, op, typ, 9))
		}
		for _, q := range []string{"SELECT x::integer FROM xid_smoke WHERE id=99", "SELECT y::oid FROM xid_smoke WHERE id=3"} {
			src, dst := "xid", "integer"
			if strings.Contains(q, "y::") {
				src, dst = "xid8", "oid"
			}
			xidReferenceAnalysisError(t, p, b, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type " + src + " to " + dst, Position: "9"}))
		}
		if !b.PG {
			for _, typ := range []string{"xid", "cid", "xid8"} {
				for _, q := range []string{"CREATE TABLE xid_array_boundary(v " + typ + "[])", "SELECT NULL::" + typ + "[]", "SELECT '{}'::" + typ + "[]"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: strconv.Itoa(strings.LastIndex(q, typ) + 1)})
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					if strings.HasPrefix(q, "CREATE TABLE ") {
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			xidReferenceAnalysisError(t, p, b, fixture.ImplicitError("SELECT xmin FROM xid_smoke", fixture.Diagnostic{Code: "42703", Message: `column "xmin" does not exist`, Position: "8"}))
		}
	})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE xid_smoke"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual smoke dump replay", err)
		}
	}
	for _, b := range p.Backends() {
		xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT id,x,y,c FROM xid_smoke ORDER BY id", []string{"id", "x", "y", "c"}, []string{"INT4", "XID", "XID8", "CID"}, want))
	}
}
