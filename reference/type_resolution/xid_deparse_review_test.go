package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func xidReviewDeparseRows(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	steps := []fixture.Step{
		fixture.Select("SELECT adnum,pg_get_expr(adbin,adrelid) AS v FROM pg_catalog.pg_attrdef WHERE adrelid='xid_review_deparse'::regclass::oid ORDER BY adnum", []string{"adnum", "v"}, []string{"INT2", "TEXT"}, [][]any{
			{int64(1), "'4294967295'::xid"}, {int64(2), "'4294967295'::cid"}, {int64(3), "'18446744073709551615'::xid8"}, {int64(5), "('4294967297'::xid8)::xid"},
		}),
		// PostgreSQL omits constant NULL defaults, even with an explicit scalar cast.
		fixture.Select("SELECT adnum FROM pg_catalog.pg_attrdef WHERE adrelid='xid_review_deparse'::regclass::oid AND adnum=4", []string{"adnum"}, []string{"INT2"}, nil),
		fixture.Select("SELECT conname AS name,pg_get_expr(conbin,conrelid) AS expr,pg_get_constraintdef(oid) AS def FROM pg_catalog.pg_constraint WHERE conrelid='xid_review_deparse'::regclass::oid ORDER BY conname", []string{"name", "expr", "def"}, []string{"NAME", "TEXT", "TEXT"}, [][]any{
			{"xid_review_c_check", "(c = '4294967295'::cid)", "CHECK ((c = '4294967295'::cid))"},
			{"xid_review_n_check", "(n = NULL::xid)", "CHECK ((n = NULL::xid))"},
			{"xid_review_x_check", "(x = '4294967295'::xid)", "CHECK ((x = '4294967295'::xid))"},
			{"xid_review_y_check", "(y >= '9223372036854775808'::xid8)", "CHECK ((y >= '9223372036854775808'::xid8))"},
		}),
	}
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		stmt, err := b.DB.PrepareContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		if err = stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceXIDPublicDeparse(t *testing.T) {
	p := fixture.NewScenario(t)
	xidReferenceBackends(t, p, func(t *testing.T, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE xid_review_deparse(x XID DEFAULT '4294967295'::xid,c CID DEFAULT '4294967295'::cid,y XID8 DEFAULT '18446744073709551615'::xid8,n XID DEFAULT NULL::xid,low XID DEFAULT ('4294967297'::xid8)::xid,CONSTRAINT xid_review_x_check CHECK(x='4294967295'::xid),CONSTRAINT xid_review_c_check CHECK(c='4294967295'::cid),CONSTRAINT xid_review_y_check CHECK(y>='9223372036854775808'::xid8),CONSTRAINT xid_review_n_check CHECK(n = NULL::xid))", 0))
		xidReviewDeparseRows(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO xid_review_deparse DEFAULT VALUES", 1))
		xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT x,c,y,n,low FROM xid_review_deparse", []string{"x", "c", "y", "n", "low"}, []string{"XID", "CID", "XID8", "XID", "XID"}, [][]any{{"4294967295", "4294967295", "18446744073709551615", nil, "1"}}))
		if !b.PG {
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
			xidReviewDeparseRows(t, p, b)
			xidReferenceRows(t, p.Ctx, b, b.DB, fixture.Select("SELECT x,c,y,n,low FROM xid_review_deparse", []string{"x", "c", "y", "n", "low"}, []string{"XID", "CID", "XID8", "XID", "XID"}, [][]any{{"4294967295", "4294967295", "18446744073709551615", nil, "1"}}))
		}
	})
}
