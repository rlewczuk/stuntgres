package reference_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimestampPrecisionNoticePhase(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, record)
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("CREATE TABLE anchor(n INTEGER)", 0))
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			stmt, e := conn.PrepareContext(ctx, "SELECT TIMESTAMP(7) '2000-01-01 00:00:00.1234567' FROM anchor")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("prepare warning got %#v want %#v", notices, want)
			}
			notices = nil
			for range 2 {
				rows, err := stmt.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceTimestampConsume(t, rows, postgres, fixture.Select("", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 123457000, time.UTC)}}))
				fixture.TemporalRecorded(t, record, fixture.Select("SELECT TIMESTAMP(7) '2000-01-01 00:00:00.1234567' FROM anchor", []string{"timestamp"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2000, 1, 1, 0, 0, 0, 123457000, time.UTC)}}))
			}
			if len(notices) != 0 {
				t.Fatal("execution repeated warning", notices)
			}
			utilitySQL := "CREATE TABLE t(v TIMESTAMP(7))"
			utility, e := conn.PrepareContext(ctx, utilitySQL)
			if e != nil {
				t.Fatal(e)
			}
			if len(notices) != 0 {
				t.Fatal("utility warned at prepare", notices)
			}
			if _, e = utility.ExecContext(ctx); e != nil {
				t.Fatal(e)
			}
			utility.Close()
			want = append(want, want[0])
			want[0].Position = strconv.Itoa(strings.Index(utilitySQL, "TIMESTAMP") + 1)
			want[1].Position = ""
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("utility execution warning got %#v want %#v", notices, want)
			}
			notices = nil
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("CREATE TABLE IF NOT EXISTS t(v TIMESTAMP(100))", 0))
			skip := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
			if !reflect.DeepEqual(notices, skip) {
				t.Fatal(notices, skip)
			}
			notices = nil
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("SET client_min_messages TO error", 0))
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("CREATE TABLE silent(v TIMESTAMP(100))", 0))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec("SET client_min_messages TO notice", 0))
			loudSQL := "CREATE TABLE loud(v TIMESTAMP(100))"
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec(loudSQL, 0))
			for i := range want {
				want[i].Message = "TIMESTAMP(100) precision reduced to maximum allowed, 6"
			}
			want[0].Position = strconv.Itoa(strings.Index(loudSQL, "TIMESTAMP") + 1)
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			notices = nil
			manySQL := "CREATE TABLE many(a TIMESTAMP(7),b TIMESTAMP(8))"
			fixture.TemporalRunStep(t, ctx, conn, record, fixture.Exec(manySQL, 0))
			phaseWant := []fixture.Diagnostic{}
			for _, phase := range []bool{true, false} {
				for i, prec := range []int{7, 8} {
					n := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(" + strconv.Itoa(prec) + ") precision reduced to maximum allowed, 6"}
					if phase {
						offset := strings.Index(manySQL, "TIMESTAMP")
						if i == 1 {
							offset = strings.LastIndex(manySQL, "TIMESTAMP")
						}
						n.Position = strconv.Itoa(offset + 1)
					}
					phaseWant = append(phaseWant, n)
				}
			}
			if !reflect.DeepEqual(notices, phaseWant) {
				t.Fatal(notices, phaseWant)
			}
		})
	}
}
