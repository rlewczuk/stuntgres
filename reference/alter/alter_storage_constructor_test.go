package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// PostgreSQL REL_18_6 catalog/index.c constructs tsvector GiST keys as
// gtsvector (3642): type-default PLAIN storage and default compression. A later
// explicit property ALTER instead changes only the first simple occurrence.
func TestReferenceAlterStorageConstructorSubstitutedKey(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) {
				if prepared {
					s = fixture.Prepared(s)
				}
				p.Step(t, s)
			}
			run(fixture.Exec("CREATE TABLE storage_sub(v tsvector)", 0))
			run(fixture.Exec("ALTER TABLE storage_sub ALTER v SET STORAGE EXTERNAL, ALTER v SET COMPRESSION pglz", 0))
			run(fixture.Exec("CREATE INDEX storage_sub_idx ON storage_sub USING gist(v,v)", 0))
			run(storageMetadata("storage_sub", []any{"v", "e", "p"}))
			run(storageMetadata("storage_sub_idx", []any{"v", "p", ""}, []any{"v1", "p", ""}))
			run(fixture.Select("SELECT atttypid::bigint FROM pg_attribute WHERE attrelid='storage_sub_idx'::regclass AND attnum>0 ORDER BY attnum", []string{"atttypid"}, []string{"INT8"}, [][]any{{int64(3642)}, {int64(3642)}}))
			run(fixture.Exec("ALTER TABLE storage_sub ALTER v SET STORAGE EXTENDED, ALTER v SET COMPRESSION pglz", 0))
			run(storageMetadata("storage_sub", []any{"v", "x", "p"}))
			run(storageMetadata("storage_sub_idx", []any{"v", "x", "p"}, []any{"v1", "p", ""}))
			// Repeating a heap no-op must neither reset the explicit override nor
			// change the untouched second occurrence's constructor defaults.
			run(fixture.Exec("ALTER TABLE storage_sub ALTER v SET STORAGE EXTENDED, ALTER v SET COMPRESSION pglz", 0))
			run(storageMetadata("storage_sub_idx", []any{"v", "x", "p"}, []any{"v1", "p", ""}))
		})
	}
}

// Creating before setting heap compression isolates the missing Dump repair
// from constructor compression bugs: saved first-key x/p equals the heap, but
// replay's CREATE INDEX produces p/default and therefore still needs a repair.
func TestReferenceAlterStorageConstructorSubstitutedReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE storage_sub(v tsvector)", 0))
	p.Step(t, fixture.Exec("CREATE INDEX storage_sub_idx ON storage_sub USING gist(v,v)", 0))
	p.Step(t, fixture.Exec("CREATE INDEX storage_sub_single_idx ON storage_sub USING gist(v)", 0))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_sub ALTER v SET STORAGE EXTENDED, ALTER v SET COMPRESSION pglz", 0)))
	saved := []fixture.Step{
		storageMetadata("storage_sub", []any{"v", "x", "p"}),
		storageMetadata("storage_sub_idx", []any{"v", "x", "p"}, []any{"v1", "p", ""}),
		// Unlike the duplicate index, this unchanged singleton label cannot
		// select the name-repair branch and mask a missing property repair.
		storageMetadata("storage_sub_single_idx", []any{"v", "x", "p"}),
		fixture.Select("SELECT attnum,atttypid::bigint FROM pg_attribute WHERE attrelid='storage_sub_idx'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "atttypid"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(3642)}, {int64(2), int64(3642)}}),
	}
	for _, s := range saved {
		p.Step(t, s)
	}
	before := storageDump(t, p)
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
	if err != nil {
		t.Fatal(err)
	}
	cloneNative := sql.OpenDB(connector)
	defer cloneNative.Close()
	clone, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Restore(p.Ctx, cloneNative, bytes.NewReader(before)); err != nil {
		t.Fatal("fresh-driver Restore", err)
	}
	for _, s := range saved {
		fixture.RunStep(t, p.Ctx, clone, recorder, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, cloneNative, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(redump.Bytes(), before) {
		t.Fatal("fresh-driver substituted-key redump changed")
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_sub CASCADE"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(before)); err != nil {
			t.Fatal("PostgreSQL substituted-key replay", err)
		}
	}
	for _, s := range saved {
		p.Step(t, s)
	}
	if !bytes.Equal(storageDump(t, p), before) {
		t.Fatal("source substituted-key redump changed")
	}
}

// LIKE INCLUDING INDEXES is a new constructor against the destination heap,
// not a copy of the source index's explicit first-only ALTER overrides.
// REL_18_6 parser/parse_utilcmd.c routes cloned indexes through index creation.
func TestReferenceAlterStorageConstructorLikeIndexes(t *testing.T) {
	for _, kind := range []struct {
		name, typ, method, initialStorage, secondStorage string
	}{
		{"simple_duplicates", "text", "btree", "MAIN", "m"},
		{"substituted_duplicates", "tsvector", "gist", "DEFAULT", "p"},
	} {
		t.Run(kind.name, func(t *testing.T) {
			for _, policy := range []struct {
				name, options, storage, compression string
			}{
				{"neither", "", "x", ""},
				{"storage_only", " INCLUDING STORAGE", "e", ""},
				{"compression_only", " INCLUDING COMPRESSION", "x", "p"},
				{"both", " INCLUDING STORAGE INCLUDING COMPRESSION", "e", "p"},
			} {
				t.Run(policy.name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					p.Step(t, fixture.Exec("CREATE TABLE storage_source(v "+kind.typ+")", 0))
					p.Step(t, fixture.Exec("ALTER TABLE storage_source ALTER v SET STORAGE "+kind.initialStorage, 0))
					p.Step(t, fixture.Exec("CREATE INDEX storage_source_idx ON storage_source USING "+kind.method+"(v,v)", 0))
					p.Step(t, fixture.Exec("ALTER TABLE storage_source ALTER v SET STORAGE EXTERNAL, ALTER v SET COMPRESSION pglz", 0))
					p.Step(t, storageMetadata("storage_source", []any{"v", "e", "p"}))
					p.Step(t, storageMetadata("storage_source_idx", []any{"v", "e", "p"}, []any{"v1", kind.secondStorage, ""}))
					create := fixture.Exec("CREATE TABLE storage_copy(LIKE storage_source INCLUDING INDEXES"+policy.options+")", 0)
					if policy.name == "storage_only" || policy.name == "both" {
						create = fixture.Prepared(create)
					}
					p.Step(t, create)
					p.Step(t, storageMetadata("storage_copy", []any{"v", policy.storage, policy.compression}))
					storage, compression, typ := policy.storage, policy.compression, int64(25)
					if kind.method == "gist" {
						storage, compression, typ = "p", "", int64(3642)
					}
					// Resolve the destination's actual index through pg_index, so
					// this proves both cloned occurrences without guessing its name.
					p.Step(t, fixture.Select("SELECT a.attnum,a.attname,a.atttypid::bigint,a.attstorage::text,a.attcompression::text FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indexrelid WHERE i.indrelid='storage_copy'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attnum", "attname", "atttypid", "attstorage", "attcompression"}, []string{"INT2", "NAME", "INT8", "TEXT", "TEXT"}, [][]any{
						{int64(1), "v", typ, storage, compression},
						{int64(2), "v1", typ, storage, compression},
					}))
					// Neither destination construction nor its selected exclusions
					// may mutate the source's divergent saved index properties.
					p.Step(t, storageMetadata("storage_source_idx", []any{"v", "e", "p"}, []any{"v1", kind.secondStorage, ""}))
				})
			}
		})
	}
}
