package proc

import "testing"

func TestNativeDeclarationCallPreorder(t *testing.T) {
	parts := []DeclarationPart{DeclarationText("("), DeclarationFunction("lower", "25"), DeclarationText("("), DeclarationArgument(0), DeclarationText(") "), DeclarationOp("||", 25, 25), DeclarationText(" "), DeclarationFunction("lower", "25"), DeclarationText("("), DeclarationArgument(0), DeclarationText("))")}
	body, err := NewRoutineDeclarationBody(DeclarationRecipe{Return: true, Parts: parts})
	if err != nil {
		t.Fatal(err)
	}
	recipe, ok := RoutineDeclarationRecipe(body)
	if !ok {
		t.Fatal("missing immutable recipe")
	}
	expected := [3]int{5, 1, 7}
	if recipe.CallCount() != len(expected) {
		t.Fatal("lost repeated selected occurrences", recipe.CallCount())
	}
	for ordinal, index := range expected {
		got, part := recipe.CallPart(ordinal)
		if got != index || part != parts[index] {
			t.Fatalf("preorder %d: got part %d, want %d", ordinal, got, index)
		}
	}
	// The source owner copied the input parts once; caller mutation cannot relink
	// its two equal lower() occurrences or replace the operator's typed identity.
	parts[1] = DeclarationFunction("upper", "25")
	_, first := recipe.CallPart(1)
	if first.Name != "lower" {
		t.Fatal("caller mutation changed immutable call source")
	}
}

func TestNativeDeclarationCallMissingTypedSource(t *testing.T) {
	_, err := NewRoutineDeclarationBody(DeclarationRecipe{Return: true, Source: "SELECT upper($1)", Parts: []DeclarationPart{DeclarationFunction("lower", "25"), DeclarationText("("), DeclarationArgument(0), DeclarationText(")")}})
	if err == nil {
		t.Fatal("unmatched typed routine became a claimed selected occurrence")
	}
}

func TestNativeDeclarationSelfQualification(t *testing.T) {
	parts := []DeclarationPart{
		DeclarationText("SELECT "), DeclarationFunction("lower", "25"),
		DeclarationText("("), DeclarationSelfRoutineName("wrapper"),
		DeclarationText("."), DeclarationArgument(0), DeclarationText(")"),
	}
	body, err := NewRoutineDeclarationBody(DeclarationRecipe{
		Parts: parts, Source: "SELECT lower(value)",
	})
	if err != nil {
		t.Fatal(err)
	}
	recipe, ok := RoutineDeclarationRecipe(body)
	if !ok || recipe.CallCount() != 1 {
		t.Fatal("formal qualification became a callable occurrence", recipe.CallCount())
	}
	index, part := recipe.CallPart(0)
	if index != 1 || part != parts[1] {
		t.Fatal("lost actual typed routine", index, part)
	}
}
