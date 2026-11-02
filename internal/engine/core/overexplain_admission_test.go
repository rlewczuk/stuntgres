package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func explainRejectFoldPublication(t *testing.T, e *core.Engine, s *session.Session, statement parser.Statement, description *core.Description) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		t.Fatal(err)
	}
	owner, accepted := description.Folds, description.Folds.Current()
	publication, err := core.BeginFoldPublication(description, statement, environment, auth.Catalog, nil)
	if publication != nil || !errors.Is(err, ids.ErrCatalogReplaced) || description.Folds != owner || owner.Live() || owner.Current().Generation != accepted.Generation || len(owner.Current().Entries) != 0 {
		t.Fatal("EXPLAIN admitted a real child fold publication", publication, err)
	}
}

func explainPreparedActivation(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	type held struct {
		statement   parser.Statement
		description core.Description
		typ         sqltype.ID
	}
	var handles []held
	defer func() {
		for i := range handles {
			e.RetirePreparedDescription(&handles[i].description)
		}
	}()
	for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
		statement, description := enginetest.PreparedDescription(t, e, s, "EXPLAIN (DEBUG, FORMAT "+format+") SELECT id FROM explain_boundary_foreign")
		typ := sqltype.Text
		if format == "JSON" {
			typ = sqltype.JSON
		} else if format == "XML" {
			typ = sqltype.XML
		}
		if len(description.Results) != 1 || description.Results[0].Usage.Type.ID() != typ || description.Folds.Live() || description.Folds.Current().Generation != 0 {
			t.Fatal("Describe validated DEBUG early or retained child execution")
		}
		handles = append(handles, held{statement: statement, description: description, typ: typ})
		r, err := enginetest.PreparedExecute(e, s, statement, &handles[len(handles)-1].description)
		var sqlerr *sqlstate.Error
		if r.Rows != nil {
			r.Rows.Close()
			t.Fatal("unloaded DEBUG published output")
		}
		if !errors.As(err, &sqlerr) || sqlerr.Code != "42601" || sqlerr.Message != `unrecognized EXPLAIN option "debug"` {
			t.Fatal("unloaded Execute did not check current physical recognition", err)
		}
	}
	if err := s.Settings.LoadOverexplain(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := range handles {
		h := &handles[i]
		owner := h.description.Folds
		r, err := enginetest.PreparedExecute(e, s, h.statement, &h.description)
		if err != nil {
			t.Fatal("same prepared definition did not see LOAD", err)
		}
		if r.Rows == nil || r.Rows.Schema()[0].Usage.Type.ID() != h.typ {
			t.Fatal("incorrect post-LOAD result codec")
		}
		r.Rows.Close()
		if h.description.Folds != owner || owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
			t.Fatal("activation replaced/published prepared child owner")
		}
		explainRejectFoldPublication(t, e, s, h.statement, &h.description)
	}
}
