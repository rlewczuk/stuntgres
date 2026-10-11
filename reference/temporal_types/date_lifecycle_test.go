package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReferenceDateConcurrentPreparedOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE d(v DATE)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO d VALUES('epoch'),('2000-02-29'),('infinity')", 3))
			query := "SELECT v FROM d WHERE v=$1"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			var wg sync.WaitGroup
			start := make(chan struct{})
			for _, tc := range []struct {
				arg  string
				want any
			}{{"epoch", "1970-01-01"}, {"2000-02-29", "2000-02-29"}, {"infinity", "infinity"}} {
				if b.PG {
					switch tc.arg {
					case "epoch":
						tc.want = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
					case "2000-02-29":
						tc.want = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)
					}
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					// Each concurrent consumer records its own owned result stream.
					recorder := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
					for range 8 {
						fixture.DateRunStep(t, p.Ctx, held, recorder, fixture.Select(query, []string{"v"}, []string{"DATE"}, [][]any{{tc.want}}, tc.arg))
					}
				}()
			}
			close(start)
			wg.Wait()
		})
	}
}

// The reader blocks external I/O, not an engine lock; release always precedes join.
type dateGatedReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	payload *strings.Reader
}

func (r *dateGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	if r.payload == nil {
		return 0, io.EOF
	}
	return r.payload.Read(p)
}

func TestReferenceDateCanceledStagedRestorePreservesSnapshot(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE d(v DATE)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO d VALUES('epoch')", 1))
	}
	query := "SELECT v FROM d"
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord)
	old, err := p.Memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	reader := &dateGatedReader{started: make(chan struct{}), release: make(chan struct{}), payload: strings.NewReader("CREATE TABLE public.replaced (v INT); INSERT INTO public.replaced VALUES (99);")}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, p.Native, reader) }()
	select {
	case <-reader.started:
	case <-ctx.Done():
		close(reader.release)
		<-done
		t.Fatal("restore reader not reached")
	}
	cancel()
	close(reader.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s := fixture.Select(query, []string{"v"}, []string{"DATE"}, [][]any{{"1970-01-01"}})
	fixture.DateConsume(t, old, false, s)
	fixture.DateRecorded(t, p.MemoryRecord, false, s)
	for _, b := range p.Backends() {
		if b.PG {
			s.Rows = [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}
		}
		fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func TestReferenceDateRetainedCursorsAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
	run(fixture.Exec(`CREATE TABLE d(v "date")`, 0))
	run(fixture.Exec("INSERT INTO d VALUES('-infinity'),('4714-11-24 BC'),('0001-02-29 BC'),('epoch'),('epoch'),('5874897-12-31'),('infinity'),(NULL)", 8))
	want := [][]any{{"-infinity"}, {"4714-11-24 BC"}, {"0001-02-29 BC"}, {"1970-01-01"}, {"1970-01-01"}, {"5874897-12-31"}, {"infinity"}, {nil}}
	query := "SELECT v FROM d ORDER BY v"
	retainedRecord := &fixture.Recorder{RawBackend: fixture.RawNative}
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, retainedRecord)
	old, err := p.Memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	bad := "CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),('bad');"
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: fixture.Position(utf8.RuneCountInString("CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),(") + 1)}
	fixture.State(t, err, "22007")
	fixture.AssertDiagnostic(t, err, &diagnostic)
	s := fixture.Select(query, []string{"v"}, []string{"DATE"}, want)
	run(s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("failed Restore changed snapshot", err)
	}
	run(fixture.Exec("INSERT INTO d VALUES('2000-01-01')", 1))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.DateConsume(t, old, false, s)
	fixture.DateRecorded(t, retainedRecord, false, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	held, err := p.Memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v DATE); INSERT INTO replacement VALUES('2000-01-01');")); err != nil {
		t.Fatal(err)
	}
	fixture.DateConsume(t, held, false, s)
	fixture.DateRecorded(t, retainedRecord, false, s)
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Memory.ExecContext(canceled, "INSERT INTO replacement VALUES('epoch')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT v FROM replacement", []string{"v"}, []string{"DATE"}, [][]any{{"2000-01-01"}}))
	iteration, stop := context.WithCancel(ctx)
	rows, err := p.Memory.QueryContext(iteration, "SELECT v FROM replacement")
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer rows.Close()
	stop()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
