package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independent expectations: PostgreSQL REL_18_6 tablecmds.c,
// MergeInheritedAttribute (3469-3495) and storage_name (2468-2484):
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/commands/tablecmds.c
// Storage compares effective uppercase modes; compression merges independently,
// ignoring unspecified methods but rejecting unequal explicit methods. These
// new expectations are source-grounded; first pinned-oracle confirmation is
// prospective, not an observation made while authoring this test-first round.
func TestReferenceAlterStorageInheritanceConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, firstStorage, secondStorage, firstCompression, secondCompression string
		message, detail string
	}{
		{"default_storage", "EXTERNAL", "DEFAULT", "default", "default", `inherited column "t" has a storage parameter conflict`, "EXTERNAL versus EXTENDED"},
		{"explicit_storage", "EXTERNAL", "MAIN", "default", "default", `inherited column "t" has a storage parameter conflict`, "EXTERNAL versus MAIN"},
		{"explicit_compression", "EXTERNAL", "EXTERNAL", "pglz", "lz4", `column "t" has a compression method conflict`, "pglz versus lz4"},
	} {
		for _, reverse := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reverse=%v/prepared=%v", tc.name, reverse, prepared), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, relation := range []string{"storage_p1", "storage_p2"} {
						p.Step(t, fixture.Exec("CREATE TABLE "+relation+"(t text)", 0))
						p.Step(t, fixture.Exec("INSERT INTO "+relation+" VALUES('"+relation+"')", 1))
					}
					p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p1 ALTER t SET STORAGE "+tc.firstStorage+", ALTER t SET COMPRESSION "+tc.firstCompression, 0))
					p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p2 ALTER t SET STORAGE "+tc.secondStorage+", ALTER t SET COMPRESSION "+tc.secondCompression, 0))
					parents, detail := "storage_p1,storage_p2", tc.detail
					if reverse {
						parents = "storage_p2,storage_p1"
						switch tc.name {
						case "default_storage":
							detail = "EXTENDED versus EXTERNAL"
						case "explicit_storage":
							detail = "MAIN versus EXTERNAL"
						case "explicit_compression":
							detail = "lz4 versus pglz"
						}
					}
					// ImplicitError asserts both ERROR severities and every portable
					// optional field: only Code, Message and Detail are populated.
					s := fixture.ImplicitError("CREATE TABLE storage_child() INHERITS("+parents+")", fixture.Diagnostic{
						Code: "42804", Message: tc.message, Detail: detail,
					})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					p.Step(t, s)
					p.Step(t, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='storage_child'", []string{"relname"}, []string{"NAME"}, nil))
					p.Step(t, fixture.Select("SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent='storage_p1'::regclass OR i.inhparent='storage_p2'::regclass ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, nil))
					storageCodes := map[string]string{"EXTERNAL": "e", "DEFAULT": "x", "MAIN": "m"}
					compressionCodes := map[string]string{"default": "", "pglz": "p", "lz4": "l"}
					p.Step(t, storageMetadata("storage_p1", []any{"t", storageCodes[tc.firstStorage], compressionCodes[tc.firstCompression]}))
					p.Step(t, storageMetadata("storage_p2", []any{"t", storageCodes[tc.secondStorage], compressionCodes[tc.secondCompression]}))
					for _, relation := range []string{"storage_p1", "storage_p2"} {
						p.Step(t, fixture.Select("SELECT t FROM ONLY "+relation, []string{"t"}, []string{"TEXT"}, [][]any{{relation}}))
					}
					// Reuse the failed destination: no hidden relation shell or
					// inherited descriptor may have escaped candidate failure.
					p.Step(t, fixture.Exec("CREATE TABLE storage_child(healthy integer)", 0))
					p.Step(t, fixture.Exec("INSERT INTO storage_child VALUES(17)", 1))
					p.Step(t, fixture.Select("SELECT healthy FROM storage_child", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(17)}}))
				})
			}
		}
	}
}

func TestReferenceAlterStorageInheritanceMerge(t *testing.T) {
	for _, tc := range []struct {
		name, storage, storageCode, first, second, compression string
	}{
		{"explicit_then_default", "EXTERNAL", "e", "pglz", "default", "p"},
		{"lz4_then_default", "EXTERNAL", "e", "lz4", "default", "l"},
		{"equal_explicit", "EXTERNAL", "e", "pglz", "pglz", "p"},
		{"both_default", "EXTERNAL", "e", "default", "default", ""},
		{"sparse_type_default", "DEFAULT", "x", "pglz", "default", "p"},
	} {
		for _, reverse := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reverse=%v/prepared=%v", tc.name, reverse, prepared), func(t *testing.T) {
					p := fixture.NewScenario(t)
					p.Step(t, fixture.Exec("CREATE TABLE storage_p1(t text)", 0))
					p.Step(t, fixture.Exec("CREATE TABLE storage_p2(t text)", 0))
					p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p1 ALTER t SET STORAGE "+tc.storage+", ALTER t SET COMPRESSION "+tc.first, 0))
					p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p2 ALTER t SET STORAGE "+tc.storage+", ALTER t SET COMPRESSION "+tc.second, 0))
					parents := "storage_p1,storage_p2"
					if reverse {
						parents = "storage_p2,storage_p1"
					}
					s := fixture.Exec("CREATE TABLE storage_child() INHERITS("+parents+")", 0)
					s.Prepared = prepared
					p.Step(t, s)
					p.Step(t, storageMetadata("storage_child", []any{"t", tc.storageCode, tc.compression}))
					compressionCodes := map[string]string{"default": "", "pglz": "p", "lz4": "l"}
					p.Step(t, storageMetadata("storage_p1", []any{"t", tc.storageCode, compressionCodes[tc.first]}))
					p.Step(t, storageMetadata("storage_p2", []any{"t", tc.storageCode, compressionCodes[tc.second]}))
					for _, step := range storageInheritanceEdges(reverse) {
						p.Step(t, step)
					}
					p.Step(t, fixture.Exec("INSERT INTO storage_child VALUES('child'),(NULL)", 2))
					p.Step(t, fixture.Select("SELECT t FROM storage_child ORDER BY t NULLS LAST", []string{"t"}, []string{"TEXT"}, [][]any{{"child"}, {nil}}))
				})
			}
		}
	}
}

func storageInheritanceEdges(reverse bool) []fixture.Step {
	first, second := "storage_p1", "storage_p2"
	if reverse {
		first, second = second, first
	}
	return []fixture.Step{
		fixture.Select("SELECT p.relname,i.inhseqno FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent WHERE i.inhrelid='storage_child'::regclass ORDER BY i.inhseqno", []string{"relname", "inhseqno"}, []string{"NAME", "INT4"}, [][]any{{first, int64(1)}, {second, int64(2)}}),
		fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='storage_child'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"t", false, int64(2)}}),
	}
}

// A legal final state can no longer be constructed from the parents' final
// policies. Replay must construct compatible parents/child first, then install
// ONLY completions, without losing either inheritance edge or index policies.
func TestReferenceAlterStorageInheritanceReplay(t *testing.T) {
	for _, property := range []string{"storage", "compression"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", property, reverse), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE storage_p1(t text)", 0))
				p.Step(t, fixture.Exec("CREATE TABLE storage_p2(t text)", 0))
				compression := ""
				if property == "compression" {
					compression = "p"
					for _, relation := range []string{"storage_p1", "storage_p2"} {
						p.Step(t, fixture.Exec("ALTER TABLE ONLY "+relation+" ALTER t SET COMPRESSION pglz", 0))
					}
				}
				parents := "storage_p1,storage_p2"
				if reverse {
					parents = "storage_p2,storage_p1"
				}
				p.Step(t, fixture.Prepared(fixture.Exec("CREATE TABLE storage_child() INHERITS("+parents+")", 0)))
				// Indexes predate divergence: parent ALTER selects just the
				// first duplicate occurrence; the child index stays unchanged.
				p.Step(t, fixture.Exec("CREATE INDEX storage_p1_idx ON storage_p1(t,t)", 0))
				p.Step(t, fixture.Exec("CREATE INDEX storage_p2_idx ON storage_p2(t)", 0))
				p.Step(t, fixture.Exec("CREATE INDEX storage_child_idx ON storage_child(t)", 0))
				firstStorage, firstCompression := "e", compression
				action := "STORAGE EXTERNAL"
				if property == "compression" {
					firstStorage, firstCompression = "x", "l"
					action = "COMPRESSION lz4"
				}
				p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE ONLY storage_p1 ALTER t SET "+action, 0)))
				for _, relation := range []string{"storage_p1", "storage_p2", "storage_child"} {
					p.Step(t, fixture.Exec("INSERT INTO "+relation+" VALUES('"+relation+"')", 1))
				}
				saved := []fixture.Step{
					storageMetadata("storage_p1", []any{"t", firstStorage, firstCompression}),
					storageMetadata("storage_p2", []any{"t", "x", compression}),
					storageMetadata("storage_child", []any{"t", "x", compression}),
					storageMetadata("storage_p1_idx", []any{"t", firstStorage, firstCompression}, []any{"t1", "x", compression}),
					storageMetadata("storage_p2_idx", []any{"t", "x", compression}),
					storageMetadata("storage_child_idx", []any{"t", "x", compression}),
				}
				saved = append(saved, storageInheritanceEdges(reverse)...)
				for _, relation := range []string{"storage_p1", "storage_p2", "storage_child"} {
					saved = append(saved, fixture.Select("SELECT t FROM ONLY "+relation, []string{"t"}, []string{"TEXT"}, [][]any{{relation}}))
				}
				for _, relation := range []string{"storage_p1", "storage_p2"} {
					saved = append(saved, fixture.Select("SELECT t FROM "+relation+" ORDER BY t", []string{"t"}, []string{"TEXT"}, [][]any{{"storage_child"}, {relation}}))
				}
				for _, s := range saved {
					p.Step(t, s)
				}
				before := storageDump(t, p)
				// Replay destinations are independent subtests so a native
				// constructor failure cannot hide the real PostgreSQL witness.
				t.Run("fresh_native", func(t *testing.T) {
					connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
					if err != nil {
						t.Fatal(err)
					}
					cloneNative := sql.OpenDB(connector)
					defer cloneNative.Close()
					clone, recorder := fixture.RecordedNativePool(t, connector)
					if err := stuntgres.Restore(p.Ctx, cloneNative, bytes.NewReader(before)); err != nil {
						t.Fatal("fresh-driver inheritance Restore", err)
					}
					for _, s := range saved {
						fixture.RunStep(t, p.Ctx, clone, recorder, s)
					}
					var redump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, cloneNative, &redump); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(redump.Bytes(), before) {
						t.Fatal("fresh-driver inheritance redump changed")
					}
				})
				if p.Postgres != nil {
					t.Run("postgres_replay", func(t *testing.T) {
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_child,storage_p1,storage_p2 CASCADE"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, string(before)); err != nil {
							t.Fatal("PostgreSQL inheritance dump replay", err)
						}
						for _, backend := range p.Backends() {
							if backend.PG {
								for _, s := range saved {
									fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
								}
							}
						}
					})
				}
				if !bytes.Equal(storageDump(t, p), before) {
					t.Fatal("source inheritance redump changed")
				}
			})
		}
	}
}

func TestReferenceAlterStorageInheritanceLocal(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("reverse=%v/prepared=%v", reverse, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, name := range []string{"storage_p1", "storage_p2", "storage_p3"} {
					p.Step(t, fixture.Exec("CREATE TABLE "+name+"(i integer,t text,n numeric)", 0))
				}
				p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p2 ALTER t SET COMPRESSION lz4", 0))
				parents := "storage_p1,storage_p2,storage_p3"
				if reverse { parents = "storage_p3,storage_p2,storage_p1" }
				s := fixture.Exec("CREATE TABLE storage_local(i integer,t text,n numeric) INHERITS("+parents+")", 0)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, storageMetadata("storage_local", []any{"i", "p", ""}, []any{"t", "x", "l"}, []any{"n", "m", ""}))
				p.Step(t, fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='storage_local'::regclass AND attnum>0 ORDER BY attnum",
					[]string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"},
					[][]any{{"i", true, int64(3)}, {"t", true, int64(3)}, {"n", true, int64(3)}}))
				for _, name := range []string{"storage_p1", "storage_p3"} {
					p.Step(t, storageMetadata(name, []any{"i", "p", ""}, []any{"t", "x", ""}, []any{"n", "m", ""}))
				}
			})
		}
	}
}

// Retired policies must wait for their constructors, but cannot join the late
// live-policy pass after DROP. A grandchild keeps the constructor frontier real.
func TestReferenceAlterStorageInheritanceHistory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, name := range []string{"storage_p1", "storage_p2"} {
		p.Step(t, fixture.Exec("CREATE TABLE "+name+"(t text,gone text)", 0))
		p.Step(t, fixture.Exec("ALTER TABLE ONLY "+name+" ALTER gone SET STORAGE EXTERNAL, ALTER gone SET COMPRESSION pglz", 0))
	}
	p.Step(t, fixture.Exec("CREATE TABLE storage_child() INHERITS(storage_p1,storage_p2)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE storage_grand() INHERITS(storage_child)", 0))
	p.Step(t, fixture.Exec("CREATE INDEX storage_history_idx ON storage_child(t)", 0))
	p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p1 ALTER t SET STORAGE EXTERNAL", 0))
	p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_p2 ALTER t SET COMPRESSION lz4", 0))
	for _, name := range []string{"storage_p1", "storage_p2"} {
		p.Step(t, fixture.Exec("ALTER TABLE "+name+" DROP COLUMN gone", 0))
	}
	var saved []fixture.Step
	for _, name := range []string{"storage_p1", "storage_p2", "storage_child", "storage_grand"} {
		p.Step(t, fixture.Exec("INSERT INTO "+name+" VALUES('"+name+"')", 1))
		storage, compression := "x", ""
		if name == "storage_p1" { storage = "e" }
		if name == "storage_p2" { compression = "l" }
		saved = append(saved,
			fixture.Select("SELECT attnum,attisdropped,attstorage::text,attcompression::text FROM pg_attribute WHERE attrelid='"+name+"'::regclass AND attnum>0 ORDER BY attnum",
				[]string{"attnum", "attisdropped", "attstorage", "attcompression"}, []string{"INT2", "BOOL", "TEXT", "TEXT"},
				[][]any{{int64(1), false, storage, compression}, {int64(2), true, "e", "p"}}),
			fixture.Select("SELECT t FROM ONLY "+name, []string{"t"}, []string{"TEXT"}, [][]any{{name}}))
	}
	saved = append(saved, storageMetadata("storage_history_idx", []any{"t", "x", ""}),
		fixture.Select("SELECT c.relname,p.relname,i.inhseqno FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent WHERE c.relname IN ('storage_child','storage_grand') ORDER BY c.relname,i.inhseqno",
			[]string{"relname", "relname", "inhseqno"}, []string{"NAME", "NAME", "INT4"},
			[][]any{{"storage_child", "storage_p1", int64(1)}, {"storage_child", "storage_p2", int64(2)}, {"storage_grand", "storage_child", int64(1)}}))
	for _, s := range saved { p.Step(t, s) }
	before := storageDump(t, p)
	t.Run("fresh_native", func(t *testing.T) {
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
		if err != nil { t.Fatal(err) }
		native := sql.OpenDB(connector)
		defer native.Close()
		clone, recorder := fixture.RecordedNativePool(t, connector)
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(before)); err != nil { t.Fatal(err) }
		for _, s := range saved { fixture.RunStep(t, p.Ctx, clone, recorder, s) }
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, native, &after); err != nil { t.Fatal(err) }
		if !bytes.Equal(before, after.Bytes()) { t.Fatal("history component redump changed") }
	})
	if p.Postgres != nil {
		t.Run("postgres_replay", func(t *testing.T) {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_grand,storage_child,storage_p1,storage_p2 CASCADE"); err != nil { t.Fatal(err) }
			if _, err := p.Postgres.ExecContext(p.Ctx, string(before)); err != nil { t.Fatal(err) }
			for _, backend := range p.Backends() {
				if backend.PG {
					for _, s := range saved { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				}
			}
		})
	}
}
