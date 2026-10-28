package schemacmds_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFDWSupportOrder(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	before := e.Catalog.State
	for _, tc := range []struct{ clauses, code, message, repeated string }{
		{"NO HANDLER NO VALIDATOR NO HANDLER", "42601", "conflicting or redundant options", "NO HANDLER"},
		{"NO VALIDATOR NO HANDLER NO VALIDATOR", "42601", "conflicting or redundant options", "NO VALIDATOR"},
		{"NO HANDLER HANDLER missing", "42601", "conflicting or redundant options", "HANDLER missing"},
		{"HANDLER missing NO HANDLER", "42883", "function missing() does not exist", ""},
		{"VALIDATOR missing NO VALIDATOR", "42883", "function missing(text[], oid) does not exist", ""},
		{"VALIDATOR missing_v HANDLER missing_h", "42883", "function missing_v(text[], oid) does not exist", ""},
		{"HANDLER missing_h VALIDATOR missing_v", "42883", "function missing_h() does not exist", ""},
	} {
		t.Run(tc.clauses, func(t *testing.T) {
			q := `CREATE FOREIGN DATA WRAPPER "é" ` + tc.clauses
			statements, err := parser.Parse(q)
			if err != nil {
				t.Fatal("semantic support check ran in parser", err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			e.Mu.Lock()
			stage.AuthorizeLocked(auth, op.Session)
			_, _, err = fdw.StageDDL(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, statements[0].(*parser.ForeignDDL), sqltype.InputEnvironment{})
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatal("support precedence", err)
			}
			position := ""
			if tc.repeated != "" {
				position = strconv.Itoa(len([]rune(q[:strings.LastIndex(q, tc.repeated)])) + 1)
			}
			if diagnostic.Position != position || e.Catalog.State != before {
				t.Fatal("position or rejected-stage publication", diagnostic, position)
			}
		})
	}
}
