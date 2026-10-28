//go:build !noservertests

package reference_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Reset/Restore have no PostgreSQL API analogue. Their SQL and byte-stream
// witnesses are explicit native-control phases of the shared authority scenario.
func clientEncodingAuthorityReplacement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		if b.PG {
			continue
		}
		t.Run(b.Name, func(t *testing.T) {
			for _, restore := range []bool{false, true} {
				t.Run(map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
					c, err := p.Native.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Fatal(q, err)
						}
					}
					exec("CREATE TABLE ce_replace(v text)")
					exec("INSERT INTO ce_replace VALUES('é')")
					exec("SET client_encoding='LATIN1'")
					var accepted bytes.Buffer
					r, err := stuntgres.CopyTo(p.Ctx, c, "COPY ce_replace TO STDOUT", &accepted)
					if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) || !bytes.Equal(accepted.Bytes(), []byte{0xe9, '\n'}) {
						t.Fatalf("old accepted bytes %x %+v %v", accepted.Bytes(), r, err)
					}
					rows, err := c.QueryContext(p.Ctx, "SELECT v FROM ce_replace")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					reader := &clientEncodingReplacementReader{entered: make(chan struct{}), release: make(chan struct{}), data: []byte{0xe9, '\n'}}
					type outcome struct {
						result stuntgres.CopyResult
						err    error
					}
					done := make(chan outcome, 1)
					joined := false
					released := false
					defer func() {
						if !released {
							close(reader.release)
						}
						if !joined {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								t.Error("replacement COPY worker did not join")
							}
						}
					}()
					go func() {
						r, err := stuntgres.CopyFrom(p.Ctx, c, "COPY ce_replace FROM STDIN", reader)
						done <- outcome{r, err}
					}()
					select {
					case <-reader.entered:
					case result := <-done:
						joined = true
						t.Fatalf("COPY did not reach admitted callback %+v", result)
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					}
					// The public callback runs unlocked; replacement must finish while
					// that callback is held, then veto this old command's publication.
					if restore {
						err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
					} else {
						err = stuntgres.Reset(p.Ctx, p.Native)
					}
					if err != nil {
						t.Fatal(err)
					}
					close(reader.release)
					released = true
					var result outcome
					select {
					case result = <-done:
						joined = true
					case <-p.Ctx.Done():
						t.Fatal("replacement COPY join", p.Ctx.Err())
					}
					if result.result != (stuntgres.CopyResult{}) {
						t.Fatalf("stale generation published %+v", result)
					}
					fixture.TransactionOrdinary(t, result.err, "stuntgres: catalog replaced before publication")
					columns, err := rows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "v" {
						t.Fatalf("old columns %q %v", columns, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
						t.Fatalf("old types %+v %v", types, err)
					}
					if !rows.Next() {
						t.Fatal("old row missing", rows.Err())
					}
					var value string
					if err := rows.Scan(&value); err != nil || value != "é" {
						t.Fatalf("old Unicode %q %v", value, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("old EOF", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(accepted.Bytes(), []byte{0xe9, '\n'}) {
						t.Fatal("accepted bytes mutated by replacement")
					}
					// The same physical session admits current catalog providers afresh.
					exec("SET client_encoding='LATIN1'")
					if restore {
						var count int
						if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_replace").Scan(&count); err != nil || count != 1 {
							t.Fatalf("replacement input prefix %d %v", count, err)
						}
						exec("DROP TABLE ce_replace")
					}
					var answer int
					if err := c.QueryRowContext(p.Ctx, "SELECT 42").Scan(&answer); err != nil || answer != 42 {
						t.Fatalf("replacement reuse %d %v", answer, err)
					}
				})
			}
		})
	}
}

type clientEncodingReplacementReader struct {
	entered, release chan struct{}
	data             []byte
	announced        bool
}

func (r *clientEncodingReplacementReader) Read(p []byte) (int, error) {
	if !r.announced {
		r.announced = true
		close(r.entered)
		<-r.release
	}
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
