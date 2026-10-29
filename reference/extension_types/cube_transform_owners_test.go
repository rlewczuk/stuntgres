package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"stuntgres/reference/fixture"
)

var cubeTransformOwnerSignatures = map[string]string{
	"cube_subset":  "public.cube, integer[]",
	"cube_union":   "public.cube, public.cube",
	"cube_inter":   "public.cube, public.cube",
	"cube_enlarge": "public.cube, double precision, integer",
}

func cubeTransformOwnerScript(t *testing.T, script string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	start, end, data := fixture.CatalogExtensionOwnerDirective(t, script, "cube", true)
	var err error
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, member := range members {
		var name, kind, signature string
		if err := json.Unmarshal(member["name"], &name); err != nil {
			t.Fatal(err)
		}
		want, selected := cubeTransformOwnerSignatures[name]
		if !selected {
			continue
		}
		if err := json.Unmarshal(member["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(member["signature"], &signature); err != nil {
			t.Fatal(err)
		}
		if seen[name] || kind != "procedure" || signature != want {
			t.Fatal("transform member owner identity", name, kind, signature)
		}
		seen[name] = true
		member["owner"] = json.RawMessage(`"cube_transform_owner"`)
	}
	if len(seen) != len(cubeTransformOwnerSignatures) {
		t.Fatal("missing transform owners", seen)
	}
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + marker + base64.StdEncoding.EncodeToString(data) + script[end:]
}

func cubeTransformPGOwners(t *testing.T, p *fixture.Pair, db fixture.Executor, rec *fixture.Recorder) {
	t.Helper()
	for _, signature := range []string{"cube_subset(public.cube,integer[])", "cube_union(public.cube,public.cube)", "cube_inter(public.cube,public.cube)", "cube_enlarge(public.cube,double precision,integer)"} {
		fixture.RunStep(t, p.Ctx, db, rec, fixture.Exec("ALTER FUNCTION public."+signature+" OWNER TO cube_transform_owner", 0))
	}
}

func cubeTransformCheckOwners(t *testing.T, p *fixture.Pair, db fixture.Executor, rec *fixture.Recorder) {
	t.Helper()
	for _, name := range []string{"cube_subset", "cube_union", "cube_inter", "cube_enlarge"} {
		s := fixture.Select("SELECT p.proowner=r.oid AS owner FROM pg_proc p JOIN pg_roles r ON r.rolname='cube_transform_owner' WHERE p.pronamespace='public'::regnamespace AND p.proname="+cubeLiteral(name), []string{"owner"}, []string{"BOOL"}, [][]any{{true}})
		fixture.RunStep(t, p.Ctx, db, rec, s)
	}
}
