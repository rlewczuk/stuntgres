package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native admission cancellation is an ordinary Go API boundary. This does not
// assert timing or transport behavior for fast lib/pq requests.
func ismn13Cancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	cast := ean13Held(t, p, c, "SELECT $1::ismn13")
	insert := ean13Held(t, p, c, "INSERT INTO ismn13_values VALUES(99,$1)")
	stamps := map[string][4]int64{}
	for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
		stamps[typ] = isbnIdentityStamp(t, p, c, typ)
	}
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(p.Ctx)
		want := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(p.Ctx, time.Unix(0, 0))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		check := func(err error) {
			t.Helper()
			var state interface{ SQLState() string }
			if !errors.Is(err, want) || errors.As(err, &state) {
				t.Fatalf("canceled boundary = %v, want ordinary %v", err, want)
			}
		}
		rows, err := c.QueryContext(ctx, cast.SQL, "M123456780!")
		if rows != nil { rows.Close() }
		check(err)
		rows, err = cast.Statement.QueryContext(ctx, "M123456780!")
		if rows != nil { rows.Close() }
		check(err)
		stmt, err := c.PrepareContext(ctx, cast.SQL)
		if stmt != nil { stmt.Close() }
		check(err)
		_, err = c.ExecContext(ctx, insert.SQL, "M123456780!")
		check(err)
		_, err = insert.Statement.ExecContext(ctx, "M123456780!")
		check(err)
		check(stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE EXTENSION isn;")))
		<-ctx.Done()
		if !errors.Is(ctx.Err(), want) { t.Fatal(ctx.Err()) }
		cancel()
		for typ, stamp := range stamps {
			if got := isbnIdentityStamp(t, p, c, typ); got != stamp {
				t.Fatalf("cancellation changed %s identity: %v, want %v", typ, got, stamp)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Ean13Select(b, "SELECT v FROM ismn13_values ORDER BY id", []string{"v"}, []string{"ISMN13"}, [][]any{{nil}, {nil}, {nil}, {nil}, {nil}, {nil}, {nil}}, []bool{true}))
	}
}
