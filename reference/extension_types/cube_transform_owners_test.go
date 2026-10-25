package reference_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var cubeTransformOwnerSignatures = map[string]string{
	"cube_subset":"cube,1007",
	"cube_union":"cube,cube",
	"cube_inter":"cube,cube",
	"cube_enlarge":"cube,701,23",
}

func cubeTransformOwnerScript(t *testing.T,script string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v2 "
	start := strings.Index(script,marker)
	if start < 0 { t.Fatal("missing transform owner manifest") }
	offset := strings.IndexByte(script[start:],'\n')
	if offset < 0 { t.Fatal("unterminated transform owner manifest") }
	end := start+offset
	data,err := base64.StdEncoding.DecodeString(script[start+len(marker):end])
	if err != nil { t.Fatal(err) }
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data,&record); err != nil { t.Fatal(err) }
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"],&members); err != nil { t.Fatal(err) }
	seen := map[string]bool{}
	for _, member := range members {
		var name,kind,signature string
		if err := json.Unmarshal(member["name"],&name); err != nil { t.Fatal(err) }
		want,selected := cubeTransformOwnerSignatures[name]
		if !selected { continue }
		if err := json.Unmarshal(member["kind"],&kind); err != nil { t.Fatal(err) }
		if err := json.Unmarshal(member["signature"],&signature); err != nil { t.Fatal(err) }
		if seen[name] || kind != "procedure" || signature != want { t.Fatal("transform member owner identity",name,kind,signature) }
		seen[name] = true
		member["owner"] = json.RawMessage(`"cube_transform_owner"`)
	}
	if len(seen) != len(cubeTransformOwnerSignatures) { t.Fatal("missing transform owners",seen) }
	record["members"],err = json.Marshal(members)
	if err != nil { t.Fatal(err) }
	data,err = json.Marshal(record)
	if err != nil { t.Fatal(err) }
	return script[:start]+marker+base64.StdEncoding.EncodeToString(data)+script[end:]
}

func cubeTransformPGOwners(t *testing.T,p *fixture.Pair,db fixture.Executor,rec *fixture.Recorder) {
	t.Helper()
	for _, signature := range []string{"cube_subset(public.cube,integer[])","cube_union(public.cube,public.cube)","cube_inter(public.cube,public.cube)","cube_enlarge(public.cube,double precision,integer)"} {
		fixture.RunStep(t,p.Ctx,db,rec,fixture.Exec("ALTER FUNCTION public."+signature+" OWNER TO cube_transform_owner",0))
	}
}

func cubeTransformCheckOwners(t *testing.T,p *fixture.Pair,db fixture.Executor,rec *fixture.Recorder) {
	t.Helper()
	for _, name := range []string{"cube_subset","cube_union","cube_inter","cube_enlarge"} {
		s := fixture.Select("SELECT p.proowner=r.oid AS owner FROM pg_proc p JOIN pg_roles r ON r.rolname='cube_transform_owner' WHERE p.pronamespace='public'::regnamespace AND p.proname="+cubeLiteral(name),[]string{"owner"},[]string{"BOOL"},[][]any{{true}})
		fixture.RunStep(t,p.Ctx,db,rec,s)
	}
}
