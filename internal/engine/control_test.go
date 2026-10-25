package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestControlsRoundTripAndFailurePreservation(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE "a table" ("quote""col" TEXT, n BIGINT, b BOOLEAN, i INTEGER NOT NULL)`)
	run(t, e, `CREATE TABLE empty (x TEXT)`)
	run(t, e, `INSERT INTO "a table" VALUES ($1, $2, $3, $4)`, "line\nO'Brien\\tail", int64(-9223372036854775808), false, 0)
	run(t, e, `INSERT INTO "a table" VALUES ('',NULL,NULL,1)`)
	var dump bytes.Buffer
	if err := Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	copy := New()
	if err := Restore(context.Background(), copy, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := Dump(context.Background(), copy, &again); err != nil {
		t.Fatal(err)
	}
	if dump.String() != again.String() {
		t.Fatalf("unstable dump:\n%s\n%s", dump.String(), again.String())
	}
	want := [][]driver.Value{{"line\nO'Brien\\tail", int64(-9223372036854775808), false, int64(0)}, {"", nil, nil, int64(1)}}
	if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip rows: %v", got)
	}
	for _, script := range []string{"CREATE TABLE other (i INTEGER NOT NULL); INSERT INTO other VALUES (1),(NULL);", "CREATE TABLE other (i INTEGER); CREATE TABLE other (i TEXT);", "SELECT * FROM empty;", "CREATE TABLE other (i INTEGER); INSERT INTO other VALUES ($1);", "CREATE TABLE other (i INTEGER); nonsense"} {
		if err := Restore(context.Background(), copy, strings.NewReader(script)); err == nil {
			t.Fatalf("restore accepted %q", script)
		}
		if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
			t.Fatalf("failed restore lost sentinel: %v", got)
		}
	}
	if err := Restore(context.Background(), copy, failingReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader error: %v", err)
	}
	if err := Dump(context.Background(), copy, failingWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []func() error{func() error { return Reset(ctx, copy) }, func() error { return Restore(ctx, copy, strings.NewReader("")) }, func() error {
		_, err := testExecute(copy, ctx, statement(t, `INSERT INTO "a table" VALUES ('lost',0,TRUE,3)`), nil)
		return err
	}} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error: %v", err)
		}
	}
	if got := tuples(run(t, copy, `SELECT * FROM "a table" ORDER BY i`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed controls changed sentinel: %v", got)
	}
	if err := Restore(context.Background(), copy, strings.NewReader(" -- empty\n /* script */ ")); err != nil {
		t.Fatal(err)
	}
	_, err := testDescribe(copy, statement(t, `SELECT * FROM "a table"`))
	state(t, err, "42P01")
}
