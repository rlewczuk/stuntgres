package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func pglUnitRoots(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef, catalog.Read, catalog.Read) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2613}
	source := e.CatalogRead().MustRelation(address)
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
	object := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 920101}
	var content largeobject.Value
	content, err := content.Write(4097, "\x00\x80\xff")
	if err != nil {
		t.Fatal(err)
	}
	old := catalog.CloneState(e.Catalog.State)
	d := &catalog.LargeObject{ObjectAddress: object, ObjectRevision: 1, ObjectName: "920101", Owner: source.Owner, Content: content}
	old.SetDescriptor(object, d)
	old.BindName(catalog.DescriptorNameKey(d), object)
	next := catalog.CloneState(old)
	replacement := *d
	replacement.Content, err = content.Write(4097, "\xaa\xbb\xcc")
	if err != nil {
		t.Fatal(err)
	}
	next.SetDescriptor(object, &replacement)
	return source, ref, e.Catalog.Read(old), e.Catalog.Read(next)
}

func pglUnitPage(t *testing.T, rows [][]sqltype.Value, want string) {
	t.Helper()
	if len(rows) != 1 || len(rows[0]) != 3 {
		t.Fatalf("page bag %#v", rows)
	}
	oid, err := rows[0][0].OID()
	if err != nil || oid != 920101 {
		t.Fatal("page identity", oid, err)
	}
	page, err := rows[0][1].Integer()
	if err != nil || page != 2 {
		t.Fatal("page number", page, err)
	}
	data, err := rows[0][2].Binary()
	if err != nil || data != want {
		t.Fatalf("page bytes %x, want %x: %v", data, want, err)
	}
}

func TestPGLargeobjectCapturedRoots(t *testing.T) {
	e := New()
	source, ref, old, next := pglUnitRoots(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.Inquiry.CommentCatalog = old
	open := func() [][]sqltype.Value {
		e.Mu.Lock()
		result, err := EngineOpenCatalogSourceLocked(e, scope, ref, source)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		return result.Anonymous
	}
	frozen := open()
	pglUnitPage(t, frozen, "\x00\x00\x80\xff")
	SetScopeLargeObjectRead(scope, next)
	body := open()
	pglUnitPage(t, body, "\x00\xaa\xbb\xcc")
	SetScopeLargeObjectRead(scope, catalog.Read{})
	// Later publication/removal cannot change either retained source or fallback.
	latest := catalog.CloneState(next.State())
	latest.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 920101})
	e.Catalog.PublishLocked(latest)
	pglUnitPage(t, open(), "\x00\x00\x80\xff")
	pglUnitPage(t, frozen, "\x00\x00\x80\xff")
	pglUnitPage(t, body, "\x00\xaa\xbb\xcc")
}

func TestPGLargeobjectCanceledCapture(t *testing.T) {
	e := New()
	source, ref, old, _ := pglUnitRoots(t, e)
	produce, ok := pgcatalog.ProducerFor(2613)
	if !ok {
		t.Fatal("missing page producer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, view := range []catalog.Read{e.CatalogRead(), old} {
		result, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Owner: view.Owner(), Catalog: view, Source: source, Ref: ref}, produce)
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", result, err)
		}
	}
}

func TestPGLargeobjectProjection(t *testing.T) {
	e := New()
	source, ref, old, _ := pglUnitRoots(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	produce, _ := pgcatalog.ProducerFor(2613)
	in := pgcatalog.Input{Context: context.Background(), Owner: &e.Catalog.Owner, Catalog: old, Roles: auth.Roles, Source: source, Ref: ref}
	result, err := CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	pglUnitPage(t, result.Anonymous, "\x00\x00\x80\xff")
	empty := in
	empty.Catalog = e.CatalogRead()
	result, err = CaptureCatalogSourceRows(empty, produce)
	if err != nil || result == nil || result.Anonymous == nil || len(result.Anonymous) != 0 {
		t.Fatal("empty capture", result, err)
	}
	result.Close()
	foreign := New()
	foreignOp, foreignDone := roleOperation(t, foreign, "postgres")
	defer foreignDone()
	foreign.Mu.Lock()
	foreignAuth, err := foreign.AuthorizationLocked(foreignOp)
	foreign.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	object := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 920101}
	// Each mutation starts from the successful captured-root control above.
	withSource := func(in pgcatalog.Input, change func(*catalog.Relation)) pgcatalog.Input {
		state := catalog.CloneState(in.Catalog.State())
		replacement := *in.Source
		replacement.Columns = append([]catalog.Column(nil), in.Source.Columns...)
		change(&replacement)
		state.SetDescriptor(replacement.Address(), &replacement)
		in.Catalog, in.Source = e.Catalog.Read(state), &replacement
		return in
	}
	withObject := func(in pgcatalog.Input, change func(*catalog.LargeObject)) pgcatalog.Input {
		state := catalog.CloneState(in.Catalog.State())
		replacement := *state.Get(object).(*catalog.LargeObject)
		change(&replacement)
		state.SetDescriptor(object, &replacement)
		in.Catalog = e.Catalog.Read(state)
		return in
	}
	cases := []struct {
		name   string
		change func(pgcatalog.Input) pgcatalog.Input
	}{
		{"foreign owner", func(in pgcatalog.Input) pgcatalog.Input { in.Owner = &foreign.Catalog.Owner; return in }},
		{"foreign reference domain", func(in pgcatalog.Input) pgcatalog.Input {
			in.Ref = foreign.Catalog.Domain.Ref(in.Ref.Epoch, in.Ref.Address)
			return in
		}},
		{"wrong reference epoch", func(in pgcatalog.Input) pgcatalog.Input { in.Ref.Epoch++; return in }},
		{"wrong reference address", func(in pgcatalog.Input) pgcatalog.Input { in.Ref.Address.ObjectID++; return in }},
		{"missing source", func(in pgcatalog.Input) pgcatalog.Input { in.Source = nil; return in }},
		{"noninstalled source", func(in pgcatalog.Input) pgcatalog.Input { copy := *in.Source; in.Source = &copy; return in }},
		{"wrong column count", func(in pgcatalog.Input) pgcatalog.Input {
			return withSource(in, func(source *catalog.Relation) { source.Columns = source.Columns[:2] })
		}},
		{"foreign column type", func(in pgcatalog.Input) pgcatalog.Input {
			return withSource(in, func(source *catalog.Relation) {
				source.Columns[2].Usage.Type = foreign.CatalogRead().MustRelation(source.Address()).Columns[2].Usage.Type
			})
		}},
		{"foreign paired roles", func(in pgcatalog.Input) pgcatalog.Input { in.Roles = foreignAuth.Roles; return in }},
		{"invalid object revision", func(in pgcatalog.Input) pgcatalog.Input {
			return withObject(in, func(object *catalog.LargeObject) { object.ObjectRevision = 0 })
		}},
		{"foreign object owner", func(in pgcatalog.Input) pgcatalog.Input {
			return withObject(in, func(object *catalog.LargeObject) {
				object.Owner = foreign.CatalogRead().MustRelation(source.Address()).Owner
			})
		}},
		{"descriptor map key mismatch", func(in pgcatalog.Input) pgcatalog.Input {
			state := catalog.CloneState(in.Catalog.State())
			other := object
			other.ObjectID++
			// Keep the genuine descriptor installed at its real address so its
			// provider validation succeeds independently of this alias entry.
			state.SetDescriptor(other, state.Get(object))
			in.Catalog = e.Catalog.Read(state)
			return in
		}},
		{"typed nil object", func(in pgcatalog.Input) pgcatalog.Input {
			state := catalog.CloneState(in.Catalog.State())
			var malformed *catalog.LargeObject
			state.SetDescriptor(object, malformed)
			in.Catalog = e.Catalog.Read(state)
			return in
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := CaptureCatalogSourceRows(test.change(in), produce)
			if result != nil {
				result.Close()
				t.Fatal("malformed authority published a source")
			}
			if err == nil {
				t.Fatal("malformed authority accepted")
			}
			var sqlError interface{ SQLState() string }
			if errors.As(err, &sqlError) {
				t.Fatal("invariant error fabricated SQLSTATE", err)
			}
		})
	}
}
