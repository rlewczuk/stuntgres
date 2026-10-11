package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestConstraintRenameGrammar(t *testing.T) {
	for _, tc := range []struct {
		target         string
		only, ifExists bool
	}{
		{"t", false, false}, {"public.t", false, false}, {"t *", false, false}, {"ONLY t", true, false}, {"ONLY (t)", true, false}, {"IF EXISTS ONLY public.t", true, true},
	} {
		q := `ALTER TABLE ` + tc.target + ` RENAME CONSTRAINT "Old" TO "q""u\z"`
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		raw, ok := statements[0].(*AlterTableRenameConstraint)
		if !ok || raw.Only != tc.only || raw.IfExists != tc.ifExists || raw.Name.Parts[len(raw.Name.Parts)-1].Text != "t" || raw.Constraint.Text != "Old" || raw.NewName.Text != `q"u\z` || raw.ConstraintPos.Character == 0 {
			t.Fatal(q, raw)
		}
	}
	long := strings.Repeat("é", 40)
	statements, err := Parse(`ALTER TABLE t RENAME CONSTRAINT old TO "` + long + `"`)
	if err != nil || statements[0].(*AlterTableRenameConstraint).NewName.Text != strings.Repeat("é", 31) {
		t.Fatal("UTF8 clipping", statements, err)
	}
}

func TestConstraintRenameWholeInput(t *testing.T) {
	for _, suffix := range []string{", DROP CONSTRAINT old", " nonsense"} {
		q := "ALTER TABLE t RENAME CONSTRAINT old TO new" + suffix
		statements, err := Parse(q)
		var got *sqlstate.Error
		token := "nonsense"
		if suffix[0] == ',' {
			token = ","
		}
		if !errors.As(err, &got) || got.Code != "42601" || got.Message != `syntax error at or near "`+token+`"` || got.Position != strconv.Itoa(strings.Index(q, token)+1) || len(statements) != 0 {
			t.Fatal("valid prefix escaped full-input parsing", statements, err)
		}
	}
}
