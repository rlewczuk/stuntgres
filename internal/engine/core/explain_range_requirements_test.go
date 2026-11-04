package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestExplainRangeRequirements(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{"CREATE TABLE range_requirements(id integer PRIMARY KEY,v text)", "CREATE ROLE range_requirements_denied", "GRANT USAGE ON SCHEMA public TO range_requirements_denied"} {
		enginetest.SessionSQL(t, s, query)
	}
	type demand struct {
		privilege authorization.Privileges
		columns   []int
		whole     bool
	}
	cases := []struct {
		query          string
		target, source []demand
	}{
		{"INSERT INTO range_requirements(id,v) VALUES(1,'x') RETURNING id", []demand{{authorization.InsertPrivilege, []int{0, 1}, false}, {authorization.SelectPrivilege, []int{0}, false}}, nil},
		{"INSERT INTO range_requirements AS t(id,v) VALUES(1,'x') ON CONFLICT(id) DO UPDATE SET v=excluded.v WHERE t.v IS NULL RETURNING id", []demand{{authorization.InsertPrivilege, []int{0, 1}, false}, {authorization.SelectPrivilege, []int{0}, false}, {authorization.UpdatePrivilege, []int{1}, false}, {authorization.SelectPrivilege, []int{1}, false}, {authorization.SelectPrivilege, []int{0}, false}}, nil},
		{"UPDATE range_requirements SET v='x' WHERE id=1 RETURNING id", []demand{{authorization.UpdatePrivilege, []int{1}, false}, {authorization.SelectPrivilege, []int{0}, false}}, nil},
		{"DELETE FROM range_requirements WHERE id=1 RETURNING id", []demand{{authorization.DeletePrivilege, nil, true}, {authorization.SelectPrivilege, []int{0}, false}}, nil},
		{"MERGE INTO range_requirements t USING (VALUES(1,'x'::text)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT(id,v) VALUES(s.id,s.v)", []demand{{authorization.InsertPrivilege, []int{0, 1}, false}, {authorization.UpdatePrivilege, []int{1}, false}, {authorization.SelectPrivilege, []int{0}, false}}, nil},
		{"SELECT id FROM range_requirements", nil, []demand{{authorization.SelectPrivilege, []int{0}, false}}},
		{"SELECT count(*) FROM range_requirements", nil, []demand{{authorization.SelectPrivilege, nil, false}}},
		{"SELECT tableoid FROM range_requirements", nil, []demand{{authorization.SelectPrivilege, nil, true}, {authorization.SelectPrivilege, nil, false}}},
		{"SELECT id FROM range_requirements FOR UPDATE", nil, []demand{{authorization.SelectPrivilege, []int{0}, false}, {authorization.UpdatePrivilege, nil, false}}},
		{"WITH unused AS (SELECT v FROM range_requirements) SELECT 1", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			explainRangeOwner(t, e, s, tc.query, true, func(binding analyze.Binding, auth core.AuthorizationView, document []map[string]any) {
				statement := enginetest.Statement(t, tc.query)
				var target, source []demand
				var requests []analyze.Requirement
				if err := analyze.BindingRequirements(binding, statement, func(req analyze.Requirement) error {
					if req.Ref != binding.Ref || req.Relation != binding.Relation {
						t.Fatal("target request borrowed a different binding identity")
					}
					target = append(target, demand{req.Privilege, req.Columns, req.Whole()})
					requests = append(requests, req)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for i := range binding.QuerySources {
					leaf := &binding.QuerySources[i]
					visit := func(req analyze.Requirement) error {
						if req.Ref != leaf.Ref || req.Relation != leaf.Relation {
							t.Fatal("source request is not from the accepted occurrence")
						}
						request := req.AccessRequirement()
						if request.Object != req.Ref || request.Privileges != req.Privilege || request.ColumnDemand == req.WholeRelation || request.RowMark != req.RowMark || request.Inherit != req.Inherit || request.WholeRow != req.WholeRow || !reflect.DeepEqual(request.Columns, req.Columns) || !reflect.DeepEqual(request.SystemColumns, req.SystemColumns) {
							t.Fatal("access transport lost the semantic ACL/MAC demand", req, request)
						}
						if req.RowMark && (req.Whole() || len(request.Columns) != 0 || len(request.SystemColumns) != 0 || request.WholeRow) {
							t.Fatal("rowmark invented selected columns or whole-table ACL", request)
						}
						columns := req.Columns
						if len(columns) == 0 {
							columns = nil
						} // nil and empty are the same any-column check.
						source = append(source, demand{req.Privilege, columns, req.Whole()})
						requests = append(requests, req)
						return nil
					}
					if err := analyze.SourceReadRequirements(leaf, visit); err != nil {
						t.Fatal(err)
					}
					if err := analyze.SourceRowMarkRequirements(leaf, visit); err != nil {
						t.Fatal(err)
					}
					if leaf.AnalysisOnly && len(source) != 0 {
						t.Fatal("unused analysis edge fabricated executable demand")
					}
				}
				if !reflect.DeepEqual(target, tc.target) || !reflect.DeepEqual(source, tc.source) {
					t.Fatalf("shared request order/multiplicity: target=%v source=%v want %v/%v", target, source, tc.target, tc.source)
				}
				role, ok := auth.Roles.Lookup("range_requirements_denied")
				if !ok {
					t.Fatal("missing genuine denied role")
				}
				denied, err := auth.WithCurrentUser(role.Ref)
				if err != nil {
					t.Fatal(err)
				}
				if len(requests) != 0 {
					first := requests[0].Check(denied)
					full := analyze.CheckBinding(denied, binding, statement)
					var firstSQL, fullSQL *sqlstate.Error
					if !errors.As(first, &firstSQL) || !errors.As(full, &fullSQL) || firstSQL.Code != "42501" || !reflect.DeepEqual(firstSQL, fullSQL) {
						t.Fatal("factoring changed genuine checker error order/diagnostic", first, full)
					}
				}
				entries := explainRangeEntries(t, document)
				for _, entry := range entries {
					if entry["Kind"] != "relation" {
						continue
					}
					for _, item := range entry["Required Permissions"].([]any) {
						record := item.(map[string]any)
						if record["Principal"] != "postgres" {
							t.Fatal("captured grant actor rather than checked principal", record)
						}
					}
				}
				// Count every semantic target request, including identical SELECTs.
				if len(tc.target) != 0 {
					for _, entry := range entries {
						if entry["Role"] == "target" && len(entry["Required Permissions"].([]any)) != len(tc.target) {
							t.Fatal("range lost/duplicated target requests", entry)
						}
					}
				}
			})
		})
	}
}
