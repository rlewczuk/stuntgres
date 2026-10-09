package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceIdentityDeclarationSequenceBeforeDefaults(t *testing.T) {
	p := newReferenceScenario(t)
	for _, q := range []string{`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(CACHE 0))`, `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(CACHE 0),p INT DEFAULT 'oops')`} {
		p.step(t, referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: "CACHE (0) must be greater than zero"}))
		for _, name := range []string{"bad", "bad_id_seq"} {
			s := referenceImplicitError(`SELECT * FROM `+name, referenceDiagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
			s.selectRows = true
			p.step(t, s)
		}
	}
	p.step(t, referenceExec(`CREATE SEQUENCE side`, 0))
	q := `CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(AS bigint))`
	p.step(t, referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "conflicting or redundant options", Position: strconv.Itoa(strings.Index(q, "AS bigint") + 1)}))
	p.step(t, referenceImplicitError(`CREATE TABLE bad(p INT DEFAULT 'oops',id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME public.side))`, referenceDiagnostic{Code: "42P07", Message: `relation "side" already exists`}))
}
func TestReferenceIdentityDeclarationPersistence(t *testing.T) {
	for _, phase := range []string{"ordinary", "native_roundtrip"} {
		t.Run(phase, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec(`CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(UNLOGGED START 7))`, 0))
			for _, option := range []string{"logged", "unlogged", "LOGGED", "UNLOGGED"} {
				p.step(t, referenceImplicitError(`ALTER TABLE i ALTER id SET `+option, referenceDiagnostic{Code: "XX000", Message: `option "` + strings.ToLower(option) + `" not recognized`}))
			}
			p.step(t, referenceImplicitError(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY(LOGGED UNLOGGED))`, referenceDiagnostic{Code: "42601", Message: "conflicting or redundant options", Position: "61"}))
			if phase == "native_roundtrip" {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			p.step(t, referenceExec(`INSERT INTO i DEFAULT VALUES`, 1))
			p.step(t, referenceSelect(`SELECT * FROM i`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
