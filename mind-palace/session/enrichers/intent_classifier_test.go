package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func promptDoc(display any) session.Doc {
	return session.Doc{"line": map[string]any{"display": display, "sessionId": "x"}}
}

func intentJSON(intent string) string { return `{"intent":"` + intent + `"}` }

func TestIntentClassifierNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, IntentClassifier, "intent-classifier")
}

func TestIntentClassifierMatchesAllDocs(t *testing.T) {
	if !IntentClassifier.Matches(session.Doc{}) {
		t.Error("Matches(empty doc) = false, want true")
	}
}

func TestIntentClassifierReturnsNullIntentForNonUserPromptLines(t *testing.T) {
	got := marshalEnrichment(t, IntentClassifier, session.Doc{"line": map[string]any{
		"message": map[string]any{"role": "assistant", "content": []any{}},
	}})
	if want := `{"intent":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierReturnsNullIntentWhenDisplayAbsent(t *testing.T) {
	got := marshalEnrichment(t, IntentClassifier, session.Doc{"line": map[string]any{}})
	if want := `{"intent":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierReturnsNullIntentForNonStringOrBlankDisplay(t *testing.T) {
	for _, display := range []any{42, nil, "", "  \t\n "} {
		if got, want := marshalEnrichment(t, IntentClassifier, promptDoc(display)), `{"intent":null}`; got != want {
			t.Errorf("display %#v: got %s, want %s", display, got, want)
		}
	}
}

func TestIntentClassifierClassifiesSlashCommand(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("/clear")), intentJSON("slash_command"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesQuestionByTrailingQuestionMark(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("Is this correct?")), intentJSON("question"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesQuestionByInterrogativeOpener(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("How do I fix this?")), intentJSON("question"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesInstruction(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("Fix the bug in auth.ts")), intentJSON("instruction"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesApprovalGoForIt(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("go for it")), intentJSON("approval"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesApprovalLgtm(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("lgtm")), intentJSON("approval"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierClassifiesCorrectionStartsWithNo(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("no that is wrong")), intentJSON("correction"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestIntentClassifierNoFalsePositiveCorrectionForNoMidSentence(t *testing.T) {
	got := marshalEnrichment(t, IntentClassifier, promptDoc("Make sure no tests fail"))
	if got == intentJSON("correction") {
		t.Errorf("got %s, want anything but correction", got)
	}
}

func TestIntentClassifierFallsThroughToFeedback(t *testing.T) {
	if got, want := marshalEnrichment(t, IntentClassifier, promptDoc("Interesting approach")), intentJSON("feedback"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// Rule order and pattern details of the TypeScript classifier.
func TestIntentClassifierRulePrecedenceAndPatterns(t *testing.T) {
	cases := map[string]string{
		"  /clear  ":                 "slash_command",
		"/fix it?":                   "slash_command",
		"WHAT is this":               "question",
		"is it done":                 "question",
		"Isolate the failure":        "feedback", // "Isolate" is not the opener "is"
		"whatever you think":         "feedback", // opener needs a word boundary
		"please fix it?":             "question", // trailing ? beats instruction
		"Rename the file":            "instruction",
		"prefix the name":            "feedback", // keywords need word boundaries
		"yes":                        "approval",
		"OK":                         "approval",
		"Sure":                       "approval",
		"Looks Good to me":           "approval",
		"sounds good":                "approval",
		"proceed":                    "approval",
		"yes please":                 "feedback",
		"wrong":                      "correction",
		"That's not what I wanted":   "correction",
		"Don't do that":              "correction",
		"shouldn't be there":         "correction",
		"nope":                       "feedback",
		"no":                         "correction",
		"incorrect result":           "correction",
		"approved, but update tests": "instruction", // instruction is tested before approval
	}
	for display, want := range cases {
		t.Run(display, func(t *testing.T) {
			if got := marshalEnrichment(t, IntentClassifier, promptDoc(display)); got != intentJSON(want) {
				t.Errorf("got %s, want %s", got, intentJSON(want))
			}
		})
	}
}

func TestIntentClassifierResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, IntentResult{})
}
