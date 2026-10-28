package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent prospective expectations: REL_18_6 parse_utilcmd.c expands
// INCLUDING STORAGE into specified effective storage (even a type default),
// whereas default compression remains unspecified. MergeChildAttribute checks
// these local contributions after type/typmod/collation, with parent-first
// details and the local-column storage primary (not "inherited column").
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/parser/parse_utilcmd.c
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/commands/tablecmds.c
// The root's first pinned PostgreSQL round must confirm these expectations;
// authoring this test-first file does not claim an observed oracle result.
func TestReferenceAlterStorageLocalLikeConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, parentStorage, sourceStorage, parentCompression, sourceCompression string
		options, message, detail string
	}{
		{"default_backed_local", "EXTERNAL", "DEFAULT", "default", "default", "INCLUDING STORAGE", `column "t" has a storage parameter conflict`, "EXTERNAL versus EXTENDED"},
		{"default_backed_parent", "DEFAULT", "EXTERNAL", "default", "default", "INCLUDING STORAGE", `column "t" has a storage parameter conflict`, "EXTENDED versus EXTERNAL"},
		{"explicit_external_main", "EXTERNAL", "MAIN", "default", "default", "INCLUDING STORAGE", `column "t" has a storage parameter conflict`, "EXTERNAL versus MAIN"},
		{"explicit_main_external", "MAIN", "EXTERNAL", "default", "default", "INCLUDING STORAGE", `column "t" has a storage parameter conflict`, "MAIN versus EXTERNAL"},
		{"compression_pglz_lz4", "DEFAULT", "DEFAULT", "pglz", "lz4", "INCLUDING COMPRESSION", `column "t" has a compression method conflict`, "pglz versus lz4"},
		{"compression_lz4_pglz", "DEFAULT", "DEFAULT", "lz4", "pglz", "INCLUDING COMPRESSION", `column "t" has a compression method conflict`, "lz4 versus pglz"},
		{"storage_before_compression", "EXTERNAL", "MAIN", "pglz", "lz4", "INCLUDING STORAGE INCLUDING COMPRESSION", `column "t" has a storage parameter conflict`, "EXTERNAL versus MAIN"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				storageLocalLikeInputs(t, p, tc.parentStorage, tc.parentCompression, tc.sourceStorage, tc.sourceCompression)
				// INCLUDING INDEXES must not publish any destination index when
				// the preceding local policy merge fails.
				s := fixture.ImplicitError("CREATE TABLE storage_child(LIKE storage_template INCLUDING INDEXES "+tc.options+") INHERITS(storage_parent)", fixture.Diagnostic{
					Code: "42804", Message: tc.message, Detail: tc.detail,
				})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				// ImplicitError asserts ERROR/ERROR and all portable optional
				// fields, including absent position, hint and object fields.
				p.Step(t, s)
				storageLocalLikeAbsent(t, p)
				storageLocalLikeUnchanged(t, p, tc.parentStorage, tc.parentCompression, tc.sourceStorage, tc.sourceCompression)
				// The failed destination must be reusable, including its rowtype
				// and the ordinary cloned-index name.
				p.Step(t, fixture.Exec("CREATE TABLE storage_child(healthy integer)", 0))
				p.Step(t, fixture.Exec("CREATE INDEX storage_child_t_t1_idx ON storage_child(healthy)", 0))
				p.Step(t, fixture.Exec("INSERT INTO storage_child VALUES(17)", 1))
				p.Step(t, fixture.Select("SELECT healthy FROM storage_child", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(17)}}))
			})
		}
	}
}

func TestReferenceAlterStorageLocalLikeMerge(t *testing.T) {
	for _, tc := range []struct {
		name, parentStorage, sourceStorage, parentCompression, sourceCompression string
		options, storage, compression string
	}{
		{"default_like_retains_pglz", "DEFAULT", "DEFAULT", "pglz", "default", "INCLUDING COMPRESSION", "x", "p"},
		{"default_like_retains_lz4", "DEFAULT", "DEFAULT", "lz4", "default", "INCLUDING COMPRESSION", "x", "l"},
		{"default_both_retains_inherited", "DEFAULT", "DEFAULT", "pglz", "default", "INCLUDING STORAGE INCLUDING COMPRESSION", "x", "p"},
		{"compatible_explicit_pglz", "EXTERNAL", "EXTERNAL", "pglz", "pglz", "INCLUDING STORAGE INCLUDING COMPRESSION", "e", "p"},
		{"compatible_explicit_lz4", "MAIN", "MAIN", "lz4", "lz4", "INCLUDING STORAGE INCLUDING COMPRESSION", "m", "l"},
		{"local_explicit_compression", "EXTERNAL", "EXTERNAL", "default", "lz4", "INCLUDING STORAGE INCLUDING COMPRESSION", "e", "l"},
		{"storage_only_retains_compression", "EXTERNAL", "EXTERNAL", "pglz", "lz4", "INCLUDING STORAGE", "e", "p"},
		{"compression_only_retains_storage", "EXTERNAL", "MAIN", "pglz", "pglz", "INCLUDING COMPRESSION", "e", "p"},
		{"excluded_both_conflicts", "EXTERNAL", "MAIN", "pglz", "lz4", "EXCLUDING STORAGE EXCLUDING COMPRESSION", "e", "p"},
		{"last_exclusions_win", "EXTERNAL", "DEFAULT", "lz4", "pglz", "INCLUDING STORAGE INCLUDING COMPRESSION EXCLUDING STORAGE EXCLUDING COMPRESSION", "e", "l"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				storageLocalLikeInputs(t, p, tc.parentStorage, tc.parentCompression, tc.sourceStorage, tc.sourceCompression)
				s := fixture.Exec("CREATE TABLE storage_child(LIKE storage_template INCLUDING INDEXES "+tc.options+") INHERITS(storage_parent)", 0)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, storageMetadata("storage_child", []any{"t", tc.storage, tc.compression}))
				p.Step(t, fixture.Select("SELECT attname,attislocal,attinhcount FROM pg_attribute WHERE attrelid='storage_child'::regclass AND attnum>0 ORDER BY attnum",
					[]string{"attname", "attislocal", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"t", true, int64(1)}}))
				p.Step(t, fixture.Select("SELECT p.relname,i.inhseqno FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent WHERE i.inhrelid='storage_child'::regclass ORDER BY i.inhseqno",
					[]string{"relname", "inhseqno"}, []string{"NAME", "INT4"}, [][]any{{"storage_parent", int64(1)}}))
				// The cloned index copies the merged destination pair into both
				// keys, not the source's historical first-only ALTER override.
				p.Step(t, storageLocalLikeDestinationIndex("storage_child", tc.storage, tc.compression))
				p.Step(t, fixture.Select("SELECT t FROM ONLY storage_child", []string{"t"}, []string{"TEXT"}, nil))
				p.Step(t, fixture.Exec("INSERT INTO storage_child VALUES('child'),(NULL)", 2))
				p.Step(t, fixture.Select("SELECT t FROM ONLY storage_child ORDER BY t NULLS LAST", []string{"t"}, []string{"TEXT"}, [][]any{{"child"}, {nil}}))
				storageLocalLikeUnchanged(t, p, tc.parentStorage, tc.parentCompression, tc.sourceStorage, tc.sourceCompression)
			})
		}
	}
}

// Standalone LIKE keeps selective-copy semantics: excluded fields become type
// defaults here, unlike a local LIKE whose excluded fields retain inheritance.
func TestReferenceAlterStorageLocalLikeStandalone(t *testing.T) {
	for _, tc := range []struct {
		name, options, storage, compression string
	}{
		{"neither", "", "x", ""},
		{"storage_only", "INCLUDING STORAGE", "e", ""},
		{"compression_only", "INCLUDING COMPRESSION", "x", "p"},
		{"both", "INCLUDING STORAGE INCLUDING COMPRESSION", "e", "p"},
		{"excluded", "INCLUDING STORAGE INCLUDING COMPRESSION EXCLUDING STORAGE EXCLUDING COMPRESSION", "x", ""},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				storageLocalLikeInputs(t, p, "MAIN", "lz4", "EXTERNAL", "pglz")
				s := fixture.Exec("CREATE TABLE storage_copy(LIKE storage_template INCLUDING INDEXES "+tc.options+")", 0)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, storageMetadata("storage_copy", []any{"t", tc.storage, tc.compression}))
				p.Step(t, storageLocalLikeDestinationIndex("storage_copy", tc.storage, tc.compression))
				p.Step(t, fixture.Select("SELECT attislocal,attinhcount FROM pg_attribute WHERE attrelid='storage_copy'::regclass AND attnum>0 ORDER BY attnum",
					[]string{"attislocal", "attinhcount"}, []string{"BOOL", "INT2"}, [][]any{{true, int64(0)}}))
				p.Step(t, fixture.Select("SELECT inhseqno FROM pg_inherits WHERE inhrelid='storage_copy'::regclass", []string{"inhseqno"}, []string{"INT4"}, nil))
				p.Step(t, fixture.Select("SELECT t FROM storage_copy", []string{"t"}, []string{"TEXT"}, nil))
				storageLocalLikeUnchanged(t, p, "MAIN", "lz4", "EXTERNAL", "pglz")
			})
		}
	}
}

func storageLocalLikeInputs(t *testing.T, p *fixture.Pair, parentStorage, parentCompression, sourceStorage, sourceCompression string) {
	t.Helper()
	for _, name := range []string{"storage_parent", "storage_template"} {
		p.Step(t, fixture.Exec("CREATE TABLE "+name+"(t text)", 0))
		p.Step(t, fixture.Exec("INSERT INTO "+name+" VALUES('"+name+"')", 1))
		p.Step(t, fixture.Exec("CREATE INDEX "+name+"_idx ON "+name+"(t,t)", 0))
	}
	p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_parent ALTER t SET STORAGE "+parentStorage+", ALTER t SET COMPRESSION "+parentCompression, 0))
	p.Step(t, fixture.Exec("ALTER TABLE ONLY storage_template ALTER t SET STORAGE "+sourceStorage+", ALTER t SET COMPRESSION "+sourceCompression, 0))
}

func storageLocalLikeUnchanged(t *testing.T, p *fixture.Pair, parentStorage, parentCompression, sourceStorage, sourceCompression string) {
	t.Helper()
	storageCodes := map[string]string{"DEFAULT": "x", "EXTERNAL": "e", "MAIN": "m"}
	compressionCodes := map[string]string{"default": "", "pglz": "p", "lz4": "l"}
	for _, input := range []struct {
		name, storage, compression string
	}{
		{"storage_parent", parentStorage, parentCompression},
		{"storage_template", sourceStorage, sourceCompression},
	} {
		storage, compression := storageCodes[input.storage], compressionCodes[input.compression]
		p.Step(t, storageMetadata(input.name, []any{"t", storage, compression}))
		p.Step(t, storageMetadata(input.name+"_idx", []any{"t", storage, compression}, []any{"t1", "x", ""}))
		p.Step(t, fixture.Select("SELECT t FROM ONLY "+input.name, []string{"t"}, []string{"TEXT"}, [][]any{{input.name}}))
		p.Step(t, fixture.Select("SELECT attislocal,attinhcount FROM pg_attribute WHERE attrelid='"+input.name+"'::regclass AND attnum>0 ORDER BY attnum",
			[]string{"attislocal", "attinhcount"}, []string{"BOOL", "INT2"}, [][]any{{true, int64(0)}}))
	}
}

func storageLocalLikeDestinationIndex(relation, storage, compression string) fixture.Step {
	return fixture.Select("SELECT a.attnum,a.attname,a.attstorage::text,a.attcompression::text FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indexrelid WHERE i.indrelid='"+relation+"'::regclass AND a.attnum>0 ORDER BY a.attnum",
		[]string{"attnum", "attname", "attstorage", "attcompression"}, []string{"INT2", "NAME", "TEXT", "TEXT"},
		[][]any{{int64(1), "t", storage, compression}, {int64(2), "t1", storage, compression}})
}

func storageLocalLikeAbsent(t *testing.T, p *fixture.Pair) {
	t.Helper()
	// This complete public relation bag also excludes unnamed or renamed index
	// shells, rather than assuming the name the failed clone would have chosen.
	p.Step(t, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace ORDER BY relname", []string{"relname"}, []string{"NAME"},
		[][]any{{"storage_parent"}, {"storage_parent_idx"}, {"storage_template"}, {"storage_template_idx"}}))
	p.Step(t, fixture.Select("SELECT a.attname FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace='public'::regnamespace AND c.relname='storage_child' AND a.attnum>0 ORDER BY a.attnum",
		[]string{"attname"}, []string{"NAME"}, nil))
	p.Step(t, fixture.Select("SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid=i.inhrelid WHERE i.inhparent='storage_parent'::regclass OR i.inhparent='storage_template'::regclass ORDER BY c.relname",
		[]string{"relname"}, []string{"NAME"}, nil))
	p.Step(t, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('storage_child','_storage_child') ORDER BY typname",
		[]string{"typname"}, []string{"NAME"}, nil))
}
