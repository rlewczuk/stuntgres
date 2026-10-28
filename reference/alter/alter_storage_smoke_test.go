package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAlterStorageSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE storage_case(i integer,t text,n numeric)", 0))
	p.Step(t, fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
	p.Step(t, fixture.Exec("CREATE INDEX storage_idx ON storage_case(t)", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			query := storageInitial().Query
			s, err := c.PrepareContext(p.Ctx, query); if err != nil { t.Fatal(err) }; defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, storageInitial())
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER t SET COMPRESSION pglz", 0)))
			run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "e", "p"}, []any{"n", "m", ""}))
			run(storageMetadata("storage_idx", []any{"t", "e", "p"}))
			run(fixture.Exec("ALTER TABLE storage_case ALTER t SET STORAGE MAIN", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "m", "p"}, []any{"n", "m", ""}))
			run(fixture.Prepared(storageError("ALTER TABLE storage_case ALTER t SET STORAGE EXTERNAL, ALTER n SET COMPRESSION bogus", "22023", `invalid compression method "bogus"`)))
			run(storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "m", "p"}, []any{"n", "m", ""}))
			run(storageMetadata("storage_idx", []any{"t", "m", "p"})); run(storageRows())
		})
	}
	before := storageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil { t.Fatal(err) }
	p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "m", "p"}, []any{"n", "m", ""}))
	p.Step(t, storageMetadata("storage_idx", []any{"t", "m", "p"}))
	p.Step(t, storageRows())
}
