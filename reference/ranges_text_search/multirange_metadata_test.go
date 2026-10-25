package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, identity := range []struct {
			name          string
			oid, rangeOID int64
		}{
			{"int4multirange", 4451, 3904}, {"int8multirange", 4536, 3926}, {"nummultirange", 4532, 3906},
		} {
			var oid int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_type WHERE typname=$1 AND typnamespace='pg_catalog'::regnamespace", identity.name).Scan(&oid); err != nil || oid != identity.oid {
				t.Fatalf("%s release OID=%d want=%d err=%v", identity.name, oid, identity.oid, err)
			}
			var rangeOID int64
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT rngtypid FROM pg_catalog.pg_range WHERE rngmultitypid=$1", identity.oid).Scan(&rangeOID); err != nil || rangeOID != identity.rangeOID {
				t.Fatalf("%s range OID=%d want=%d err=%v", identity.name, rangeOID, identity.rangeOID, err)
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,a int4multirange NOT NULL,b int8multirange,c nummultirange)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE allnull(a int4multirange,b int8multirange,c nummultirange)", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO allnull VALUES (NULL,NULL,NULL)", 1))
		columns, types := []string{"k", "a", "b", "c"}, []string{"INT4", "INT4MULTIRANGE", "INT8MULTIRANGE", "NUMMULTIRANGE"}
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				rangeSelect(backend, "SELECT k,a,b,c FROM r", columns, types, nil, []bool{false, false, true, true}),
				rangeSelect(backend, "SELECT a,b,c FROM allnull", columns[1:], types[1:], [][]any{{nil, nil, nil}}, []bool{true, true, true}),
			} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES (1,'{}','{(,)}','{(4,5)}')", 1))
		check := rangeSelect(backend, "SELECT k,a,b,c FROM r ORDER BY k", columns, types, [][]any{{int64(1), "{}", "{(,)}", "{(4,5)}"}}, []bool{false, false, true, true})
		rows, err := backend.DB.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		check.CheckMetadata(t, rows)
		fixture.RangeConsumeHeld(t, rows, check)
		stmt, err := backend.DB.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		check.CheckMetadata(t, rows)
		fixture.RangeConsumeHeld(t, rows, check)
		for _, prepared := range []bool{false, true} {
			check.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, check)
			zero := rangeSelect(backend, "SELECT k,a,b,c FROM r WHERE k=0", columns, types, nil, []bool{false, false, true, true})
			zero.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, zero)
		}
	}
}
