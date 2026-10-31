package reference_test

import (
	"bytes"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShseclabelNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"shseclabel_control_role"})
 databases := []string{"shseclabel_control_db"}
 fixture.DatabaseReferenceCleanup(t, &databases)
 p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "SELECT * FROM pg_catalog.pg_shseclabel"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			check := func() {
				shseclabelShape(t, p, b, b.DB)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, shseclabelEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, shseclabelEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, shseclabelCount())
			}
			check()
            for _,s := range []fixture.Step{
                fixture.Exec("CREATE ROLE shseclabel_control_role",0),
                fixture.Exec("CREATE DATABASE shseclabel_control_db",0),
            } { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
            shseclabelCreateSentinel(t,p,b,b.DB)
            fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("COMMENT ON TABLE shseclabel_sentinel IS 'ordinary comment'",0))
            check()
            fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,shseclabelSentinel())
            if b.PG { return } // Native host replacement has no PostgreSQL equivalent.
			retained, err := p.Native.QueryContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer retained.Close()
			names, err := retained.Columns()
			if err != nil { t.Fatal(err) }
			columns, err := retained.ColumnTypes()
			if err != nil { t.Fatal(err) }
			shseclabelMetadata(t, false, columns)
			savedNames := append([]string(nil), names...)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			if dump.Len() == 0 || !bytes.Contains(dump.Bytes(), []byte("kept")) || bytes.Contains(bytes.ToUpper(dump.Bytes()), []byte("SECURITY LABEL")) { t.Fatal("dump must contain real data and no fabricated labels") }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			check()
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "shseclabel_sentinel" does not exist`)
			want.Position = "15"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT * FROM shseclabel_sentinel", want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			check()
			sentinel := fixture.Select("SELECT id,v FROM shseclabel_sentinel", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, sentinel)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(sentinel))
			afterNames, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(savedNames, afterNames) { t.Fatal("retained schema changed", afterNames, err) }
			after, err := retained.ColumnTypes()
			if err != nil || !reflect.DeepEqual(columns, after) { t.Fatal("retained descriptors changed", after, err) }
			shseclabelMetadata(t, false, after)
			if retained.Next() || retained.Err() != nil { t.Fatal("retained catalog not empty", retained.Err()) }
			if err := retained.Close(); err != nil { t.Fatal(err) }
			if !reflect.DeepEqual(savedNames, names) { t.Fatal("captured names changed after close") }
			shseclabelMetadata(t, false, columns)
			// A fresh execution of the original statement consults replacement roots.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, shseclabelEmpty(false, q))
		})
	}
}
