package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSPGiSTControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "spgist_portable")
	clean := fixture.AccessMethodReferenceCleanup(t, []string{"spgist_portable_alias"})
	p := fixture.NewScenario(t)
	clean()
	var emitted string
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := classConn(t, p, b)
			for _, q := range []string{
				"CREATE SCHEMA spgist_portable",
				"CREATE ACCESS METHOD spgist_portable_alias TYPE INDEX HANDLER pg_catalog.spghandler",
				"CREATE OPERATOR FAMILY spgist_portable.alias_family USING spgist_portable_alias",
				"CREATE OPERATOR CLASS spgist_portable.alias_ops FOR TYPE point USING spgist_portable_alias FAMILY spgist_portable.alias_family AS " + spgistPointMembers,
				"CREATE TABLE spgist_portable.alias_rows(id integer,p point)",
				"INSERT INTO spgist_portable.alias_rows VALUES(1,'(0,0)')",
				"CREATE INDEX alias_index ON spgist_portable.alias_rows USING spgist_portable_alias(p spgist_portable.alias_ops) INCLUDE(id) WITH(fillfactor=80)",
				"CREATE TYPE spgist_portable.custom_range AS RANGE(subtype=integer)",
				"CREATE DOMAIN spgist_portable.range_domain AS spgist_portable.custom_range",
				"CREATE TABLE spgist_portable.domains(id integer,r spgist_portable.range_domain)",
				"CREATE INDEX spgist_portable_domain_i ON spgist_portable.domains USING spgist(r)",
				"INSERT INTO spgist_portable.domains VALUES(1,'[1,3)')",
				"CREATE TABLE spgist_portable.t(id integer,p point,b box,g polygon,i inet,r int4range,s text,k point)",
				"INSERT INTO spgist_portable.t VALUES(1,'(0,0)','(2,2),(0,0)','((0,0),(4,0),(0,4))','10.0.0.1/24','[1,3)','a','(3,4)')",
				"CREATE INDEX spgist_portable_point ON spgist_portable.t USING spgist(p) INCLUDE(id) WITH(fillfactor=80)",
				"CREATE INDEX spgist_portable_box ON spgist_portable.t USING spgist(b)",
				"CREATE INDEX spgist_portable_poly ON spgist_portable.t USING spgist(g)",

				"CREATE INDEX spgist_portable_inet ON spgist_portable.t USING spgist(i inet_ops)",
				"CREATE INDEX spgist_portable_range ON spgist_portable.t USING spgist(r)",

				"CREATE INDEX spgist_portable_kd ON spgist_portable.t USING spgist(k kd_point_ops)",
				"CREATE INDEX spgist_portable_text ON spgist_portable.t USING spgist(s text_ops)",
				"CREATE INDEX spgist_portable_expr ON spgist_portable.t USING spgist((point(id,0))) WHERE id>0",
			} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			const query = "SELECT id,p <-> point '(0,0)' AS d FROM spgist_portable.t ORDER BY id"
			const aliasQuery = "SELECT id,p <-> point '(0,0)' AS d FROM spgist_portable.alias_rows ORDER BY id"
			want := fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}})
			held := classHeld(t, p, conn, query)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = dump.String()
				if !strings.Contains(emitted, "kd_point_ops") || !strings.Contains(emitted, "fillfactor") {
					t.Fatal("actual options absent from Dump", emitted)
				}
				rows, err := conn.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				foreign := fixture.NewFixture(t, false, true)
				target, err := foreign.Memory.Conn(foreign.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer target.Close()
				retained := spgistControlCollide(t, foreign, target, spgistControlIDs(t, p, conn, false))
				if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
					t.Fatal("foreign replay", err)
				}
				if !reflect.DeepEqual(spgistControlIDs(t, foreign, target, true), retained) {
					t.Fatal("Restore changed retained TEMP identities")
				}
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Select("SELECT v FROM pg_temp.spgist_retained_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, spgistPortableAliasMetadata(80))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Select(aliasQuery, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}}))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Exec("INSERT INTO spgist_portable.alias_rows VALUES(2,'(6,8)')", 1))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Exec("ALTER INDEX spgist_portable.alias_index SET(fillfactor=90)", 0))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Exec("REINDEX INDEX spgist_portable.alias_index", 0))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Select(aliasQuery, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(10)}}))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, spgistPortableAliasMetadata(90))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Exec("INSERT INTO spgist_portable.domains VALUES(2,'[3,5)')", 1))
				fixture.RunStep(t, foreign.Ctx, target, foreign.MemoryRecord, fixture.Select("SELECT id FROM spgist_portable.domains WHERE r @> 4 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, want)
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec("INSERT INTO spgist_portable.t(id,p,s) VALUES(2,'(3,4)','abc')", 1))
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}}))
				var source bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &source); err != nil || source.String() != emitted {
					t.Fatal("foreign replay mutated source", err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				fixture.DeleteReferenceConsume(t, false, rows, nil, want)
				bad := emitted + "CREATE INDEX late_invalid ON spgist_portable.t USING spgist(p) WITH(fillfactor=9);"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				d := fixture.ControlsReferenceDiagnostic("22023", `value 9 out of bounds for option "fillfactor"`)
				d.Detail = `Valid values are between "10" and "100".`
				fixture.AssertDiagnostic(t, err, &d)
				ioFailure := errors.New("SP-GiST input reader failed")
				if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{ioFailure}); !errors.Is(err, ioFailure) {
					t.Fatal("reader failure was not preserved", err)
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
					t.Fatal("canceled Restore was not atomic", err)
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != emitted {
					t.Fatal("invalid Restore changed destination", err)
				}
			} else {
				if emitted == "" {
					t.Fatal("missing actual native SQL")
				}
				run(fixture.Exec("DROP SCHEMA spgist_portable CASCADE", 0))
				run(fixture.Exec("DROP ACCESS METHOD spgist_portable_alias", 0))
				if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("PostgreSQL rejected actual Dump", err)
				}
			}
			classRoutes(t, p, b, conn, want)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='spgist_portable.spgist_portable_point'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fillfactor=80}"}}))
			classRoutes(t, p, b, conn, spgistPortableAliasMetadata(80))
			classRoutes(t, p, b, conn, fixture.Select(aliasQuery, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}}))
			run(fixture.Exec("INSERT INTO spgist_portable.alias_rows VALUES(2,'(3,4)'),(3,NULL)", 2))
			run(fixture.Exec("UPDATE spgist_portable.alias_rows SET p='(6,8)' WHERE id=1", 1))
			run(fixture.Exec("ALTER INDEX spgist_portable.alias_index SET(fillfactor=90)", 0))
			run(fixture.Exec("REINDEX INDEX spgist_portable.alias_index", 0))
			classRoutes(t, p, b, conn, fixture.Select(aliasQuery, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(10)}, {int64(2), float64(5)}, {int64(3), nil}}))
			classRoutes(t, p, b, conn, spgistPortableAliasMetadata(90))
			run(fixture.Exec("INSERT INTO spgist_portable.t(id,p,s) VALUES(2,'(3,4)','abc')", 1))
			run(fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}}))
		})
	}
}
