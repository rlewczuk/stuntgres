package schemacmds_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func commentDump(t *testing.T, e *core.Engine) string {
	t.Helper()
	var b bytes.Buffer
	if err := engine.Dump(context.Background(), e, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type commentGateWriter struct {
	bytes.Buffer
	entered, release chan struct{}
	first            bool
}

func (w *commentGateWriter) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.Write(p)
}

// Dump uses io.WriteString, so intercept the promoted Buffer fast path too.
func (w *commentGateWriter) WriteString(s string) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.Buffer.WriteString(s)
}

type commentGateReader struct {
	input            io.Reader
	entered, release chan struct{}
	first            bool
}

func (r *commentGateReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		close(r.entered)
		<-r.release
	}
	return r.input.Read(p)
}
func TestCommentControls(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE USER owner", "CREATE SCHEMA s AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE s.t(a INT,b TEXT)", "INSERT INTO s.t VALUES(7,'sentinel')", `COMMENT ON TABLE s.t IS E'quote\' slash\\\n雪'`, "COMMENT ON COLUMN s.t.a IS 'first'", "COMMENT ON COLUMN s.t.b IS 'second'", "RESET SESSION AUTHORIZATION", "COMMENT ON SCHEMA public IS 'bootstrap'", "COMMENT ON SCHEMA s IS 'schema'", "COMMENT ON ROLE owner IS 'role'"} {
		enginetest.Run(t, e, q)
	}
	canonical := commentDump(t, e)
	if !strings.Contains(canonical, `COMMENT ON COLUMN "s"."t"."a" IS E'first';`) || strings.Contains(canonical, "COMMENT ON ROLE") {
		t.Fatal(canonical)
	}
	w := &commentGateWriter{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- engine.Dump(context.Background(), e, w) }()
	select {
	case <-w.entered:
	case err := <-done:
		t.Fatalf("Dump returned before entering the external writer: %v", err)
	}
	enginetest.Run(t, e, "COMMENT ON TABLE s.t IS 'peer'")
	close(w.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if w.String() != canonical {
		t.Fatal("dump did not capture immutable comments before I/O")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader(canonical)); err != nil {
		t.Fatal(err, canonical)
	}
	if commentDump(t, e) != canonical {
		t.Fatal("noncanonical restored comments")
	}
	role, _ := e.Roles().Lookup("owner")
	if role.Comment != "role" {
		t.Fatal(role)
	}
	for _, suffix := range []string{"COMMENT ON COLUMN s.t.missing IS 'late';", "COMMENT ON ROLE owner IS 'late';", "SET SESSION AUTHORIZATION absent; CREATE TABLE public.late(v INT);"} {
		before := e.CatalogRead()
		rolesPublication := e.RoleStore.Published
		if err := engine.Restore(context.Background(), e, strings.NewReader(canonical+"COMMENT ON TABLE s.t IS 'earlier';"+suffix)); err == nil {
			t.Fatal("accepted invalid restore", suffix)
		}
		if before.State() != e.CatalogRead().State() || rolesPublication != e.RoleStore.Published || commentDump(t, e) != canonical {
			t.Fatal("failed restore published metadata")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &commentGateReader{input: strings.NewReader(canonical), entered: make(chan struct{}), release: make(chan struct{})}
	go func() { done <- engine.Restore(ctx, e, r) }()
	<-r.entered
	cancel()
	close(r.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if commentDump(t, e) != canonical {
		t.Fatal("canceled restore changed metadata")
	}
	foreign := engine.New()
	enginetest.Run(t, foreign, "CREATE USER owner")
	enginetest.Run(t, foreign, "COMMENT ON ROLE owner IS 'foreign'")
	enginetest.Run(t, foreign, "CREATE TABLE t(v INT)")
	enginetest.Run(t, foreign, "COMMENT ON TABLE t IS 'imported'")
	if _, err := e.ReplaceCatalog(context.Background(), foreign, foreign.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	commentWant(t, e, e.CatalogRead(), enginetest.RoleTableRef(t, e, "t"), "imported")
	role, _ = e.Roles().Lookup("owner")
	if role.Comment != "role" {
		t.Fatal("import overwrote shared comment", role)
	}
}
