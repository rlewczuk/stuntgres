package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"stuntgres/reference/fixture"
)

func copyProgressBinaryText(rows int, value string) []byte {
	var out bytes.Buffer
	out.WriteString("PGCOPY\n\xff\r\n\x00")
	binary.Write(&out, binary.BigEndian, int32(0))
	binary.Write(&out, binary.BigEndian, int32(0))
	for range rows {
		binary.Write(&out, binary.BigEndian, int16(1))
		binary.Write(&out, binary.BigEndian, int32(len(value)))
		out.WriteString(value)
	}
	binary.Write(&out, binary.BigEndian, int16(-1))
	return out.Bytes()
}

type copyProgressOutput struct {
	bytes.Buffer
	permits chan struct{}
	stop    chan struct{}
	once    sync.Once
	ctx     context.Context
}

func (w *copyProgressOutput) Write(data []byte) (int, error) {
	select {
	case <-w.stop:
	case <-w.permits:
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
	return w.Buffer.Write(data)
}
func (w *copyProgressOutput) release() { w.once.Do(func() { close(w.stop) }) }

func TestReferenceCopyProgressOutput(t *testing.T) {
	for _, tc := range []struct {
		name, query, format string
		queryOutput         bool
	}{
		{name: "table_text", query: "COPY copy_progress_output TO STDOUT"},
		{name: "query_text", query: "COPY (SELECT v FROM copy_progress_output) TO STDOUT", queryOutput: true},
		{name: "csv_header", query: "COPY copy_progress_output TO STDOUT WITH(FORMAT csv, HEADER true)", format: "csv"},
		{name: "binary", query: "COPY copy_progress_output TO STDOUT WITH(FORMAT binary)", format: "binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_output(v TEXT)")
				// 32 MiB of actual table payload keeps server-side COPY alive under genuine
				// socket backpressure; a tiny client writer gate is not an admission witness.
				value := string(bytes.Repeat([]byte{'x'}, 8192))
				const rows = 4096
				text := bytes.Repeat([]byte(value+"\n"), rows)
				result, err := fixture.CopyFrom(p.Ctx, worker, b.PG, "COPY copy_progress_output FROM STDIN", bytes.NewReader(text))
				if err != nil || result.Count != rows {
					t.Fatalf("seed COPY result=%+v err=%v", result, err)
				}
				expected := text
				if tc.format == "csv" {
					expected = append([]byte("v\n"), text...)
				}
				if tc.format == "binary" {
					expected = copyProgressBinaryText(rows, value)
				}
				id := copyProgressIdentify(t, p, worker, "copy_progress_output")
				if tc.queryOutput {
					id.relid = 0
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				output := &copyProgressOutput{permits: make(chan struct{}, 128), stop: make(chan struct{}), ctx: ctx}
				done := make(chan copyProgressOutcome, 1)
				joined := false
				defer func() {
					cancel()
					output.release()
					if !joined {
						<-done
					}
				}()
				go func() {
					result, err := fixture.CopyTo(ctx, worker, b.PG, tc.query, output)
					done <- copyProgressOutcome{result, err}
				}()
				previous := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				copyProgressVisible(t, previous, id, "COPY TO")
				for range 2 {
					for {
						// Let real output advance, then capture independently. Never infer tuple
						// completion from the client write count or require equal PG/native buffering.
						for range 128 {
							select {
							case output.permits <- struct{}{}:
							default:
							}
						}
						s, ok := copyProgressRead(t, p, observer, id.pid)
						if !ok {
							t.Fatal("COPY ended before two live checkpoints")
						}
						copyProgressVisible(t, s, id, "COPY TO")
						if s.bytes.Int64 < previous.bytes.Int64 || s.bytes.Int64 > int64(len(expected)) || s.processed.Int64 < previous.processed.Int64 || s.processed.Int64 > rows || s.excluded.Int64 != 0 || s.skipped.Int64 != 0 {
							t.Fatalf("invalid output progress previous=%+v current=%+v", previous, s)
						}
						if s.processed.Int64 > previous.processed.Int64 {
							previous = s
							break
						}
						select {
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
						default:
						}
					}
				}
				output.release()
				out := <-done
				joined = true
				if out.err != nil || out.result.Count != rows || out.result.CommandTag != fmt.Sprintf("COPY %d", rows) {
					t.Fatalf("output outcome=%+v", out)
				}
				if !bytes.Equal(output.Bytes(), expected) {
					t.Fatalf("output payload mismatch: got %d bytes want %d", output.Len(), len(expected))
				}
				copyProgressIdle(t, p, observer, id.pid)
			})
		})
	}
	for _, format := range []string{"csv", "binary"} {
		t.Run("from_"+format, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				typ := "TEXT"
				if format == "binary" {
					typ = "INT"
				}
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_format(v "+typ+")")
				id := copyProgressIdentify(t, p, worker, "copy_progress_format")
				options := "WITH(FORMAT csv, HEADER true)"
				payload := append([]byte("v\n"), bytes.Repeat([]byte("hello\n"), 1000)...)
				if format == "binary" {
					options = "WITH(FORMAT binary)"
					// One INT4 tuple is exactly ten bytes: field count, length, value.
					payload = append([]byte("PGCOPY\n\xff\r\n\x00"), make([]byte, 8)...)
					payload = append(payload, bytes.Repeat([]byte{0, 1, 0, 0, 0, 4, 0, 0, 0, 42}, 1000)...)
				}
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_format FROM STDIN "+options)
				copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				run.input.send(t, p, payload)
				s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok && s.processed.Int64 == 1000 })
				copyProgressVisible(t, s, id, "COPY FROM")
				if s.bytes.Int64 > int64(len(payload)) || s.skipped.Int64 != 0 || s.excluded.Int64 != 0 {
					t.Fatalf("framing counted as rows %+v", s)
				}
				if format == "binary" {
					run.input.send(t, p, []byte{255, 255})
				}
				run.finish(t, 1000)
				copyProgressIdle(t, p, observer, id.pid)
				var value any = "hello"
				valueType := "TEXT"
				if format == "binary" {
					value = int64(42)
					valueType = "INT4"
				}
				vacuumRun(t, p, b, observer, fixture.Select("SELECT v,count(*) AS n FROM copy_progress_format GROUP BY v", []string{"v", "n"}, []string{valueType, "INT8"}, [][]any{{value, int64(1000)}}))
			})
		})
	}
}
