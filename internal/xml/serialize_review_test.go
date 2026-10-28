package xml

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestXMLSerializeWhitespaceCallbacks(t *testing.T) {
	// Independent pinned full outputs span CR + 298/299/300/301/400 tabs,
	// unspecified/default space state and both requested modes. A 300-byte
	// complex callback's raw lookahead distinguishes the dropped/retained cases.
	runSerializeCorpus(t, func(r serializeWitness) bool { return r.Group == "review1" && r.ID < 10 })
}
func TestXMLSerializeWhitespaceQName(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool { return r.Group == "review1" && r.ID >= 10 && r.ID < 14 })
}
func TestXMLSerializeDTDEnumerationAdmission(t *testing.T) {
	runSerializeCorpus(t, func(r serializeWitness) bool { return r.Group == "review1" && r.ID >= 14 })
}
func TestXMLSerializeCancellationCallbacksAndMembers(t *testing.T) {
	// Enter repeated real callbacks and duplicate comparisons, including a long
	// token whose equality scan itself polls. These inputs remain ordinary valid
	// nonvalidating XML; failure must not publish their transient collector.
	long := strings.Repeat("n", 20000)
	for _, tc := range []struct {
		phase, input string
		after        int
	}{
		{"collectCharacters", "<a>\r" + strings.Repeat("\t", 2400) + "<b/> </a>", 8},
		{"collectEnumerationMember", "<!DOCTYPE a [<!ATTLIST a x (" + long + "|" + long + "|other) #IMPLIED>]><a/>", 8},
		{"collectEnumerationMember", "<!DOCTYPE a [<!ATTLIST a x NOTATION (" + long + "|" + long + "|other) #IMPLIED>]><a/>", 8},
	} {
		value := serializeValue(t, tc.input, Content)
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			c := &phaseContext{Context: context.Background(), phase: tc.phase, after: tc.after, cause: cause}
			out, _, err := Serialize(c, value, SerializeOptions{Indent: true})
			if !c.admitted || c.seen < tc.after || !errors.Is(err, cause) || out != "" {
				t.Fatalf("phase %s admitted %t seen %d out %q error %v", tc.phase, c.admitted, c.seen, out, err)
			}
			if value.Original() != tc.input {
				t.Fatal("failed callback changed retained input")
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("callback cancellation acquired SQLSTATE")
			}
			requireSerialized(t, serializeValue(t, "<healthy/>", Content), SerializeOptions{Indent: true}, "<healthy/>")
		}
	}
}

func TestXMLSerializeDTDEnumerationWidth(t *testing.T) {
	// Source-derived width extension of the actual first-admission goldens.
	// This fixes the semantic output independently, without a timing assertion.
	members := make([]string, 20000)
	for i := range members {
		members[i] = fmt.Sprintf("n%05d", i)
	}
	syntax := strings.Join(members, "|")
	canonical := strings.Join(members, " | ")
	for _, kind := range []string{"", "NOTATION "} {
		input := "<!DOCTYPE a [<!ATTLIST a x " + kind + "(" + syntax + "|n19999|n00000|n10000) #IMPLIED y " + kind + "(n00000|n00000|tail) #IMPLIED><!ATTLIST b x " + kind + "(n00000|next) #IMPLIED>]><a/>"
		expected := "<!DOCTYPE a [\n<!ATTLIST a x " + kind + "(" + canonical + ") #IMPLIED>\n<!ATTLIST a y " + kind + "(n00000 | tail) #IMPLIED>\n<!ATTLIST b x " + kind + "(n00000 | next) #IMPLIED>\n]>\n<a/>"
		value := serializeValue(t, input, Content)
		requireSerialized(t, value, SerializeOptions{Mode: Document, Indent: true}, expected)
		requireSerialized(t, value, SerializeOptions{Mode: Content, Indent: true}, expected+"\n")
	}
}

func TestXMLSerializeCancellationIndexedEnumeration(t *testing.T) {
	members := make([]string, 2000)
	for i := range members {
		members[i] = fmt.Sprintf("n%05d", i)
	}
	long := strings.Repeat("n", 40000)
	for _, kind := range []string{"", "NOTATION "} {
		for _, tc := range []struct {
			members string
			after   int
		}{
			{strings.Join(members, "|"), 100},
			{long + "|" + long, 4},
		} {
			input := "<!DOCTYPE a [<!ATTLIST a x " + kind + "(" + tc.members + ") #IMPLIED>]><a/>"
			value := serializeValue(t, input, Content)
			for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
				c := &phaseContext{Context: context.Background(), phase: "collectEnumerationMember", after: tc.after, cause: cause}
				out, _, err := Serialize(c, value, SerializeOptions{Indent: true})
				if out != "" || !c.admitted || c.seen < tc.after || !errors.Is(err, cause) {
					t.Fatalf("kind %q after %d admitted %t seen %d out %q error %v", kind, tc.after, c.admitted, c.seen, out, err)
				}
				if value.Original() != input {
					t.Fatal("indexed admission changed retained input")
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("indexed admission cancellation acquired SQLSTATE")
				}
				requireSerialized(t, serializeValue(t, "<healthy/>", Content), SerializeOptions{Indent: true}, "<healthy/>")
			}
		}
	}
}
