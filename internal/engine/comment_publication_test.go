package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestCommentPublicationFailures(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(v INT)")
	roleExecute(t, e, op, "COMMENT ON TABLE t IS 'old'")
	ref := roleTableRef(t, e, "t")
	before := e.CatalogRead()
	d := *before.MustRelation(ref.Address)
	d.revision++
	d.ColumnComments = map[int32]string{2: "invalid"}
	stage := e.BeginCatalogStage(before)
	if err := stage.Replace(&d); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("invalid comment attribute published")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().state != before.state {
		t.Fatal("invalid provider mutation")
	}
	original := e.catalog.nextRevision
	e.catalog.nextRevision = ids.DefinitionRevision(^uint64(0))
	if _, err := e.Execute(op, statement(t, "COMMENT ON TABLE t IS 'bad'"), 0, nil); err == nil {
		t.Fatal("revision exhaustion accepted")
	}
	e.catalog.nextRevision = original
	commentWant(t, e, e.CatalogRead(), ref, "old")
	roleExecute(t, e, op, "CREATE ROLE free")
	roles := e.roles
	e.roles = &roleCatalog{revision: ^uint64(0), root: roles.root}
	if _, err := e.Execute(op, statement(t, "COMMENT ON ROLE free IS 'bad'"), 0, nil); err == nil {
		t.Fatal("role exhaustion accepted")
	}
	e.roles = roles
	if d, _ := e.Roles().Lookup("free"); d.Comment != "" {
		t.Fatal(d)
	}
}
func TestCommentRestoreRetainsLiveSharedMetadata(t *testing.T) {
	e := New()
	run(t, e, "CREATE ROLE free")
	run(t, e, "COMMENT ON ROLE free IS 'old'")
	r := &commentGateReader{input: strings.NewReader("CREATE TABLE t(v INT); COMMENT ON TABLE t IS 'local';"), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- e.Restore(context.Background(), r) }()
	<-r.entered
	run(t, e, "COMMENT ON ROLE free IS 'peer'")
	close(r.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d, _ := e.Roles().Lookup("free"); d.Comment != "peer" {
		t.Fatal("restore overwrote live shared metadata", d)
	}
	commentWant(t, e, e.CatalogRead(), roleTableRef(t, e, "t"), "local")
	run(t, e, "CREATE USER transient")
	r = &commentGateReader{input: strings.NewReader("SET SESSION AUTHORIZATION transient; CREATE TABLE public.u(v INT); COMMENT ON TABLE public.u IS 'private';"), entered: make(chan struct{}), release: make(chan struct{})}
	before := e.CatalogRead()
	go func() { done <- e.Restore(context.Background(), r) }()
	<-r.entered
	run(t, e, "DROP ROLE transient")
	run(t, e, "CREATE USER transient")
	close(r.release)
	if err := <-done; err == nil {
		t.Fatal("restore adopted same-name replacement owner")
	}
	if e.CatalogRead().state != before.state {
		t.Fatal("conflicted restore published private comments")
	}
}
