package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentityDeclarationSequenceBeforeDefaults(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(CACHE 0))`, `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(CACHE 0),p INT DEFAULT 'oops')`} {
		p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "CACHE (0) must be greater than zero"}))
		for _, name := range []string{"bad", "bad_id_seq"} {
			s := fixture.ImplicitError(`SELECT * FROM `+name, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
			s.SelectRows = true
			p.Step(t, s)
		}
	}
	p.Step(t, fixture.Exec(`CREATE SEQUENCE side`, 0))
	q := `CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(AS bigint))`
	p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(strings.Index(q, "AS bigint") + 1)}))
	p.Step(t, fixture.ImplicitError(`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME public.side))`, fixture.Diagnostic{Code: "42P07", Message: `relation "side" already exists`}))
}
func TestReferenceIdentityDeclarationPersistence(t *testing.T) {
	for _, phase := range []string{"ordinary", "native_roundtrip"} {
		t.Run(phase, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(UNLOGGED START 7))`, 0))
			for _, option := range []string{"logged", "unlogged", "LOGGED", "UNLOGGED"} {
				p.Step(t, fixture.ImplicitError(`ALTER TABLE i ALTER id SET `+option, fixture.Diagnostic{Code: "XX000", Message: `option "` + strings.ToLower(option) + `" not recognized`}))
			}
			p.Step(t, fixture.ImplicitError(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(LOGGED UNLOGGED))`, fixture.Diagnostic{Code: "42601", Message: "conflicting or redundant options", Position: "61"}))
			if phase == "native_roundtrip" {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			p.Step(t, fixture.Exec(`INSERT INTO i DEFAULT VALUES`, 1))
			p.Step(t, fixture.Select(`SELECT * FROM i`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
