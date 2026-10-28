package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// These labels occupy the first vacating candidate either directly or only
// after CREATE INDEX regenerates a duplicate attribute's numeric suffix.
func TestReferenceAlterStorageRepairPrefixCollisions(t *testing.T) {
	for _, generated := range []bool{false, true} {
		name := "occupied_label"
		if generated {
			name = "constructor_suffix"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			queries := []string{
				"CREATE TABLE storage_collision(a text,b text)",
				"INSERT INTO storage_collision VALUES('left','right')",
			}
			var saved []fixture.Step
			if generated {
				queries = append(queries,
					"CREATE INDEX storage_collision_idx ON storage_collision(a,a)",
					"ALTER TABLE storage_collision ALTER a SET STORAGE EXTERNAL, ALTER a SET COMPRESSION pglz",
					"ALTER TABLE storage_collision RENAME a TO __stuntgres_att_0_",
				)
				saved = []fixture.Step{
					storageMetadata("storage_collision", []any{"__stuntgres_att_0_", "e", "p"}, []any{"b", "x", ""}),
					storageMetadata("storage_collision_idx", []any{"a", "e", "p"}, []any{"a1", "x", ""}),
					fixture.Select("SELECT __stuntgres_att_0_,b FROM storage_collision", []string{"__stuntgres_att_0_", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"left", "right"}}),
				}
			} else {
				queries = append(queries,
					"CREATE INDEX storage_collision_idx ON storage_collision(a,b)",
					"ALTER TABLE storage_collision ALTER a SET STORAGE EXTERNAL, ALTER a SET COMPRESSION pglz, ALTER b SET STORAGE MAIN, ALTER b SET COMPRESSION lz4",
					"ALTER TABLE storage_collision RENAME b TO __stuntgres_att_0_1",
				)
				saved = []fixture.Step{
					storageMetadata("storage_collision", []any{"a", "e", "p"}, []any{"__stuntgres_att_0_1", "m", "l"}),
					storageMetadata("storage_collision_idx", []any{"a", "e", "p"}, []any{"b", "m", "l"}),
					fixture.Select("SELECT a,__stuntgres_att_0_1 FROM storage_collision", []string{"a", "__stuntgres_att_0_1"}, []string{"TEXT", "TEXT"}, [][]any{{"left", "right"}}),
				}
			}
			for _, q := range queries {
				affected := int64(0)
				if strings.HasPrefix(q, "INSERT ") {
					affected = 1
				}
				p.Step(t, fixture.Exec(q, affected))
			}
			for _, step := range saved {
				p.Step(t, step)
			}
			// Observe the actual constructor labels too: the suffix fixture must
			// really regenerate the occupied candidate, not merely resemble it.
			p.Step(t, fixture.Exec("CREATE INDEX storage_collision_probe ON storage_collision("+map[bool]string{false: "a,__stuntgres_att_0_1", true: "__stuntgres_att_0_,__stuntgres_att_0_"}[generated]+")", 0))
			if generated {
				p.Step(t, storageMetadata("storage_collision_probe", []any{"__stuntgres_att_0_", "e", "p"}, []any{"__stuntgres_att_0_1", "e", "p"}))
			} else {
				p.Step(t, storageMetadata("storage_collision_probe", []any{"a", "e", "p"}, []any{"__stuntgres_att_0_1", "m", "l"}))
			}
			p.Step(t, fixture.Exec("DROP INDEX storage_collision_probe", 0))
			script := storageDump(t, p)
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			freshNative := sql.OpenDB(connector)
			defer freshNative.Close()
			fresh, recorder := fixture.RecordedNativePool(t, connector)
			if err := stuntgres.Restore(p.Ctx, freshNative, bytes.NewReader(script)); err != nil {
				t.Fatal("fresh native collision replay", err)
			}
			for _, step := range saved {
				fixture.RunStep(t, p.Ctx, fresh, recorder, step)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, freshNative, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(script, redump.Bytes()) {
				t.Fatal("fresh native collision replay changed the logical dump")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE storage_collision CASCADE"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, string(script)); err != nil {
					t.Fatal("PostgreSQL collision replay", err)
				}
			}
			for _, step := range saved {
				p.Step(t, step)
				p.Step(t, fixture.Prepared(step))
			}
			if !bytes.Equal(script, storageDump(t, p)) {
				t.Fatal("unstable collision redump")
			}
		})
	}
}

func TestReferenceAlterStorageRepairFailureAfterVacate(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{
		"CREATE TABLE storage_destination(a text,b text)",
		"INSERT INTO storage_destination VALUES('keep','exact')",
		"CREATE INDEX storage_destination_idx ON storage_destination(a,b)",
		"ALTER TABLE storage_destination ALTER a SET STORAGE EXTERNAL, ALTER a SET COMPRESSION pglz",
	} {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT ") {
			n = 1
		}
		p.Step(t, fixture.Exec(q, n))
	}
	saved := []fixture.Step{
		storageMetadata("storage_destination", []any{"a", "e", "p"}, []any{"b", "x", ""}),
		storageMetadata("storage_destination_idx", []any{"a", "e", "p"}, []any{"b", "x", ""}),
		fixture.Select("SELECT a,b FROM storage_destination", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"keep", "exact"}}),
	}
	for _, step := range saved {
		p.Step(t, step)
	}
	before := storageDump(t, p)
	const prefix = `CREATE TABLE public.storage_partial(a text,b text);
CREATE INDEX storage_partial_idx ON public.storage_partial(a,b);
UPDATE pg_catalog.pg_attribute SET attname='__stuntgres_att_0_1' WHERE attrelid='public.storage_partial_idx'::regclass AND attnum=1;
UPDATE pg_catalog.pg_attribute SET attname='__stuntgres_att_0_2' WHERE attrelid='public.storage_partial_idx'::regclass AND attnum=2;
`
	// Establish that vacating succeeded, rather than accepting an earlier
	// boundary rejection as evidence for the later-failure contract.
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/prefix")
	if err != nil {
		t.Fatal(err)
	}
	probeNative := sql.OpenDB(connector)
	defer probeNative.Close()
	probe, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Restore(p.Ctx, probeNative, strings.NewReader(prefix)); err != nil {
		t.Fatal("valid vacating prefix rejected", err)
	}
	fixture.RunStep(t, p.Ctx, probe, recorder, storageMetadata("storage_partial_idx", []any{"__stuntgres_att_0_1", "x", ""}, []any{"__stuntgres_att_0_2", "x", ""}))
	const failure = `ALTER TABLE ONLY public.storage_partial ALTER a SET COMPRESSION bogus;`
	want := fixture.ControlsReferenceDiagnostic("22023", `invalid compression method "bogus"`)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+failure)), &want)
	if !bytes.Equal(before, storageDump(t, p)) {
		t.Fatal("failure after successful vacating changed external destination")
	}
	for _, step := range saved {
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	p.Step(t, fixture.Select("SELECT relname FROM pg_class WHERE relname='storage_partial' OR relname='storage_partial_idx' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{}))
}
