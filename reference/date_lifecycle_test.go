package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReferenceDateConcurrentPreparedOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE d(v DATE)", 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO d VALUES('epoch'),('2000-02-29'),('infinity')", 3))
			query := "SELECT v FROM d WHERE v=$1"
			stmt, err := b.db.PrepareContext(p.ctx, query)
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
				if b.pg {
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
					recorder := &referenceRecorder{rawBackend: b.recorder.rawBackend}
					held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: query}
					for range 8 {
						referenceDateRunStep(t, p.ctx, held, recorder, referenceSelect(query, []string{"v"}, []string{"DATE"}, [][]any{{tc.want}}, tc.arg))
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
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE d(v DATE)", 0))
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO d VALUES('epoch')", 1))
	}
	query := "SELECT v FROM d"
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord)
	old, err := p.memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	reader := &dateGatedReader{started: make(chan struct{}), release: make(chan struct{}), payload: strings.NewReader("CREATE TABLE public.replaced (v INT); INSERT INTO public.replaced VALUES (99);")}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, p.native, reader) }()
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
	s := referenceSelect(query, []string{"v"}, []string{"DATE"}, [][]any{{"1970-01-01"}})
	referenceDateConsume(t, old, false, s)
	referenceDateRecorded(t, p.memoryRecord, false, s)
	for _, b := range p.backends() {
		if b.pg {
			s.rows = [][]any{{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}}
		}
		referenceDateRunStep(t, p.ctx, b.db, b.recorder, s)
	}
}

func TestReferenceDateRetainedCursorsAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
	run(referenceExec(`CREATE TABLE d(v "date")`, 0))
	run(referenceExec("INSERT INTO d VALUES('-infinity'),('4714-11-24 BC'),('0001-02-29 BC'),('epoch'),('epoch'),('5874897-12-31'),('infinity'),(NULL)", 8))
	want := [][]any{{"-infinity"}, {"4714-11-24 BC"}, {"0001-02-29 BC"}, {"1970-01-01"}, {"1970-01-01"}, {"5874897-12-31"}, {"infinity"}, {nil}}
	query := "SELECT v FROM d ORDER BY v"
	retainedRecord := &referenceRecorder{rawBackend: referenceRawNative}
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, retainedRecord)
	old, err := p.memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	bad := "CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),('bad');"
	err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad))
	diagnostic := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: referencePosition(utf8.RuneCountInString("CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('epoch'),(") + 1)}
	referenceState(t, err, "22007")
	referenceAssertDiagnostic(t, err, &diagnostic)
	s := referenceSelect(query, []string{"v"}, []string{"DATE"}, want)
	run(s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("failed Restore changed snapshot", err)
	}
	run(referenceExec("INSERT INTO d VALUES('2000-01-01')", 1))
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceDateConsume(t, old, false, s)
	referenceDateRecorded(t, retainedRecord, false, s)
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	held, err := p.memory.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v DATE); INSERT INTO replacement VALUES('2000-01-01');")); err != nil {
		t.Fatal(err)
	}
	referenceDateConsume(t, held, false, s)
	referenceDateRecorded(t, retainedRecord, false, s)
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.memory.ExecContext(canceled, "INSERT INTO replacement VALUES('epoch')"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	run(referenceSelect("SELECT v FROM replacement", []string{"v"}, []string{"DATE"}, [][]any{{"2000-01-01"}}))
	iteration, stop := context.WithCancel(ctx)
	rows, err := p.memory.QueryContext(iteration, "SELECT v FROM replacement")
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
