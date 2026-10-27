package reference_test

import (
	"bytes"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGSubscriptionRelNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			q := "SELECT * FROM pg_catalog.pg_subscription_rel"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			check := func() {
				subscriptionRelShape(t, p, b, b.DB)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, subscriptionRelEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, subscriptionRelEmpty(b.PG, q))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, subscriptionRelCount())
			}
			check()
			if b.PG { return } // Native host replacement has no PostgreSQL equivalent.
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE subrel_sentinel(id integer,v text)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO subrel_sentinel VALUES(7,'kept')", 1))
			retained, err := p.Native.QueryContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer retained.Close()
			names, err := retained.Columns()
			if err != nil { t.Fatal(err) }
			columns, err := retained.ColumnTypes()
			if err != nil { t.Fatal(err) }
			subscriptionRelMetadata(t, false, columns)
			savedNames := append([]string(nil), names...)
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
			if dump.Len() == 0 { t.Fatal("empty actual dump") }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			check()
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "subrel_sentinel" does not exist`)
			want.Position = "15"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT * FROM subrel_sentinel", want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
			check()
			sentinel := fixture.Select("SELECT id,v FROM subrel_sentinel", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, sentinel)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(sentinel))
			afterNames, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(savedNames, afterNames) { t.Fatal("retained schema changed", afterNames, err) }
			after, err := retained.ColumnTypes()
			if err != nil || !reflect.DeepEqual(columns, after) { t.Fatal("retained descriptors changed", after, err) }
			subscriptionRelMetadata(t, false, after)
			if retained.Next() || retained.Err() != nil { t.Fatal("retained catalog not empty", retained.Err()) }
			if err := retained.Close(); err != nil { t.Fatal(err) }
			if !reflect.DeepEqual(savedNames, names) { t.Fatal("captured names changed after close") }
			subscriptionRelMetadata(t, false, columns)
			// A fresh execution of the original statement consults replacement roots.
			fixture.RunStep(t, p.Ctx, held, b.Recorder, subscriptionRelEmpty(false, q))
		})
	}
}
