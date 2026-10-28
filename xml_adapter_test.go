package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

const xmlOutputAdjacency = `<?xml version="1.0" encoding="UTF8"standalone="yes"?><a/>`

func xmlAdapterDescriptor(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.XML {
			return d
		}
	}
	t.Fatal("missing executable XML descriptor")
	return nil
}
func xmlAdapterValue(t *testing.T) sqltype.Value {
	t.Helper()
	d := xmlAdapterDescriptor(t)
	v, err := d.InputWithEnvironment(context.Background(), xmlOutputAdjacency, -1, sqltype.InputEnvironment{XMLMode: sqltype.XMLDocument})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func xmlAdapterRows(ctx context.Context, values []sqltype.Value, d *sqltype.Descriptor, delivery session.OutputNotices) *rows {
	schema := make([]catalog.Column, len(values))
	for i := range schema {
		schema[i] = catalog.Column{Name: "x", Usage: d.Usage()}
	}
	cursor := exec.NewFinalRows(schema, 1)
	cursor.Values = values
	return newRows(ctx, core.Result{Rows: cursor}, delivery)
}

func TestXMLNativeAdapter(t *testing.T) {
	ctx := context.Background()
	d := xmlAdapterDescriptor(t)
	for _, raw := range []driver.Value{nil, []byte(nil)} {
		v, err := importParameter(ctx, raw, d.Usage(), sqltype.InputEnvironment{XMLMode: sqltype.XMLDocument})
		if err != nil || !v.IsNull() {
			t.Fatalf("NULL raw=%#v value=%#v error=%v", raw, v, err)
		}
	}
	bytes := []byte("<a/>")
	args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
	if err != nil {
		t.Fatal(err)
	}
	bytes[1] = 'z'
	v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
	if err != nil || got != "<a/>" || scanType(d) != reflect.TypeFor[string]() {
		t.Fatalf("output=%#v type=%v error=%v", got, scanType(d), err)
	}
	v, err = importParameter(ctx, "", d.Usage(), sqltype.InputEnvironment{})
	if err != nil || v.IsNull() {
		t.Fatalf("empty CONTENT is not NULL: %v", err)
	}
	op := &session.Operation{NoticeThreshold: session.LevelWarning}
	input := sqltype.InputEnvironment{XMLInput: session.XMLInput{Op: op}}
	err = frameImporter([]driver.Value{"<a>"})(ctx, analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{d.Usage()}}, make([]sqltype.Value, 1), input)
	var diagnostic *Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2200N" || diagnostic.Context != "unnamed portal parameter $1 = '...'" || diagnostic.Position != "" {
		t.Fatalf("native inferred XML parameter provenance: %#v error=%v", diagnostic, err)
	}
	if got := input.XMLInput.XMLInputDiagnostic(Error{}); got.Context != "" {
		t.Fatal("parameter import mutated environment", got)
	}
}

func TestXMLOutputNoticeOwnership(t *testing.T) {
	v := xmlAdapterValue(t)
	d := v.Type()
	var got []Error
	delivery := session.OutputNotices{Threshold: session.LevelWarning, Handler: func(n Error) {
		// Reenter the actual detached row adapter, without a shared owner/lock.
		peer := xmlAdapterRows(context.Background(), []sqltype.Value{sqltype.Null(d)}, d, session.OutputNotices{})
		if err := peer.Next(make([]driver.Value, 1)); err != nil {
			t.Error(err)
		}
		if err := peer.Close(); err != nil {
			t.Error(err)
		}
		got = append(got, n)
	}}
	r := xmlAdapterRows(context.Background(), []sqltype.Value{v, v}, d, delivery)
	if r.cursor.OutputEnvironment().XMLNotice != nil {
		t.Fatal("retained cursor owns output sink")
	}
	r.Columns()
	r.ColumnTypeScanType(0)
	r.ColumnTypeLength(0)
	if len(got) != 0 {
		t.Fatal("metadata emitted output")
	}
	if err := r.Next(make([]driver.Value, 2)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("field notices=%#v", got)
	}
	for _, n := range got {
		if n.Code != "XX001" || n.Message != "could not parse XML declaration in stored value" || n.Detail != "Space required." {
			t.Fatalf("notice=%#v", n)
		}
	}
	if err := r.Next(make([]driver.Value, 2)); err != io.EOF {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || r.notices.Handler != nil {
		t.Fatal("EOF/Close output or retained handler")
	}

	got = nil
	r = xmlAdapterRows(context.Background(), []sqltype.Value{v, {}}, d, delivery)
	if err := r.Next(make([]driver.Value, 2)); err == nil {
		t.Fatal("invalid later field accepted")
	}
	if len(got) != 1 || r.notices.Handler != nil {
		t.Fatal("warning before later error lost or handler retained", got)
	}

	got = nil
	r = xmlAdapterRows(context.Background(), []sqltype.Value{v}, d, session.OutputNotices{Handler: delivery.Handler, Threshold: session.LevelError})
	if err := r.Next(make([]driver.Value, 1)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatal("captured error threshold ignored")
	}
	r.Close()
	r = xmlAdapterRows(context.Background(), []sqltype.Value{sqltype.Null(d)}, d, delivery)
	if err := r.Next(make([]driver.Value, 1)); err != nil {
		t.Fatal(err)
	}
	r.Close()
	empty := exec.NewFinalRows([]catalog.Column{{Name: "x", Usage: d.Usage()}}, 0)
	r = newRows(context.Background(), core.Result{Rows: empty}, delivery)
	if err := r.Next(make([]driver.Value, 1)); err != io.EOF {
		t.Fatal(err)
	}
	r.Close()
	if len(got) != 0 {
		t.Fatal("NULL/zero rows emitted output notices", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r = xmlAdapterRows(ctx, []sqltype.Value{v}, d, delivery)
	cancel()
	if err := r.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(got) != 0 || r.notices.Handler != nil {
		t.Fatal("canceled iteration emitted or retained handler")
	}
}

func TestXMLOutputNoticeBatchAndConcurrency(t *testing.T) {
	v := xmlAdapterValue(t)
	d := v.Type()
	var counts [2]int
	cursors := make([]exec.RowResult, 2)
	for i := range cursors {
		cursors[i] = xmlAdapterRows(context.Background(), []sqltype.Value{v}, d, session.OutputNotices{}).cursor
	}
	deliveries := []session.OutputNotices{
		{Threshold: session.LevelError, Handler: func(Error) { counts[0]++ }},
		{Threshold: session.LevelWarning, Handler: func(Error) { counts[1]++ }},
	}
	r := newBatchRows(context.Background(), cursors, nil, deliveries)
	if err := r.Next(make([]driver.Value, 1)); err != nil {
		t.Fatal(err)
	}
	if err := r.NextResultSet(); err != nil {
		t.Fatal(err)
	}
	if err := r.Next(make([]driver.Value, 1)); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if counts != [2]int{0, 1} {
		t.Fatal("per-result filter not retained", counts)
	}
	for _, delivery := range deliveries {
		if delivery.Handler != nil {
			t.Fatal("batch tail retained handler")
		}
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			local := xmlAdapterRows(context.Background(), []sqltype.Value{v, v}, d, session.OutputNotices{Threshold: session.LevelWarning, Handler: func(Error) { counts[i]++ }})
			<-start
			if err := local.Next(make([]driver.Value, 2)); err != nil {
				t.Error(err)
			}
			local.Close()
		}(i)
	}
	close(start)
	wg.Wait()
	if counts != [2]int{2, 3} {
		t.Fatal("concurrent delivery crossed owners", counts)
	}
}

func TestXMLOutputNoticeCompletedRequest(t *testing.T) {
	ctx := context.Background()
	owner := NewDriver()
	c := adapterMechanicsConn(t, owner, "xml-detached-owner")
	peer := adapterMechanicsConn(t, owner, "xml-detached-owner")
	called := 0
	c.noticeHandler = func(Error) {
		called++
		if err := peer.Ping(ctx); err != nil {
			t.Error("independent physical reentry", err)
		}
	}
	request, _, _, err := c.request(ctx, "", nil, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh activity proves request released its operation/lease. Changing
	// that new owner's threshold cannot alter detached delivery configuration.
	op, err := c.session.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op.NoticeThreshold = session.LevelError
	op.Done()
	v := xmlAdapterValue(t)
	r := xmlAdapterRows(ctx, []sqltype.Value{v}, v.Type(), request.notices)
	if err := r.Next(make([]driver.Value, 1)); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("detached filter followed later operation", called)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.notices.Handler != nil {
		t.Fatal("closed rows retained handler")
	}
}
