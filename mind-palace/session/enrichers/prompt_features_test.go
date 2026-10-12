package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func promptFeaturesOf(t *testing.T, prompt string) promptFeaturesResult {
	t.Helper()
	got, err := PromptFeatures.Enrich(context.Background(), session.Doc{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          prompt,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(promptFeaturesResult)
}

func expectIntent(t *testing.T, prompt, want string) {
	t.Helper()
	got := promptFeaturesOf(t, prompt).Intent
	switch {
	case want == "" && got != nil:
		t.Errorf("%q: got intent %q, want null", prompt, *got)
	case want != "" && got == nil:
		t.Errorf("%q: got null intent, want %q", prompt, want)
	case want != "" && *got != want:
		t.Errorf("%q: got intent %q, want %q", prompt, *got, want)
	}
}

func TestPromptFeaturesNameAndCollection(t *testing.T) {
	if PromptFeatures.Name != "prompt-features" || PromptFeatures.Collection != "hook_events" || !PromptFeatures.Enabled {
		t.Errorf("unexpected metadata: %+v", PromptFeatures)
	}
}

func TestPromptFeaturesMatchesUserPromptSubmitOnly(t *testing.T) {
	if !PromptFeatures.Matches(session.Doc{"hook_event_name": "UserPromptSubmit"}) {
		t.Error("UserPromptSubmit should match")
	}
	if PromptFeatures.Matches(session.Doc{"hook_event_name": "PreToolUse"}) {
		t.Error("PreToolUse should not match")
	}
	if PromptFeatures.Matches(session.Doc{}) {
		t.Error("empty doc should not match")
	}
}

func TestPromptFeaturesEmptyPromptZeroedFields(t *testing.T) {
	r := promptFeaturesOf(t, "")
	if r.WordCount != 0 || r.IsSlashCommand || r.CommandName != nil || r.Intent != nil || r.LooksLikeTaskStart {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesMissingPromptZeroedFields(t *testing.T) {
	got, err := PromptFeatures.Enrich(context.Background(), session.Doc{"hook_event_name": "UserPromptSubmit"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(promptFeaturesResult); r != (promptFeaturesResult{}) {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesSlashCommandDetected(t *testing.T) {
	r := promptFeaturesOf(t, "/clear")
	if !r.IsSlashCommand || r.CommandName == nil || *r.CommandName != "clear" || r.Intent != nil {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesSlashCommandWithArgsExtractsNameOnly(t *testing.T) {
	r := promptFeaturesOf(t, "/reload-plugins foo")
	if r.CommandName == nil || *r.CommandName != "reload-plugins" {
		t.Errorf("got %v", r.CommandName)
	}
}

func TestPromptFeaturesBareSlashHasNullCommandName(t *testing.T) {
	r := promptFeaturesOf(t, "/")
	if !r.IsSlashCommand || r.CommandName != nil {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesSlashCommandSkipsIntentEvenWhenQuestion(t *testing.T) {
	expectIntent(t, "/help me fix this?", "")
}

func TestPromptFeaturesQuestionIntentByTrailingQuestionMark(t *testing.T) {
	expectIntent(t, "Is this correct?", "question")
}

func TestPromptFeaturesQuestionIntentByOpenerWord(t *testing.T) {
	expectIntent(t, "How do I fix this", "question")
}

func TestPromptFeaturesQuestionOpenerRequiresWordBoundary(t *testing.T) {
	expectIntent(t, "whatever", "")
	expectIntent(t, "isolate the module", "")
}

func TestPromptFeaturesQuestionOpenerMustBeAtStart(t *testing.T) {
	expectIntent(t, "please tell me how", "")
}

func TestPromptFeaturesInstructionIntent(t *testing.T) {
	expectIntent(t, "Fix the failing tests in auth.ts", "instruction")
}

func TestPromptFeaturesInstructionKeywordMatchesAnywhereAsWholeWord(t *testing.T) {
	expectIntent(t, "please RENAME it", "instruction")
	expectIntent(t, "prefix the thing", "")
	expectIntent(t, "madd", "")
}

func TestPromptFeaturesQuestionBeatsInstruction(t *testing.T) {
	expectIntent(t, "fix this?", "question")
}

func TestPromptFeaturesApprovalIntentOk(t *testing.T) {
	expectIntent(t, "ok", "approval")
}

func TestPromptFeaturesApprovalIntentLgtm(t *testing.T) {
	expectIntent(t, "lgtm", "approval")
}

func TestPromptFeaturesApprovalVariants(t *testing.T) {
	expectIntent(t, "YES", "approval")
	expectIntent(t, "Sure", "approval")
	expectIntent(t, "Looks good to me", "approval")
	expectIntent(t, "go for it", "approval")
	expectIntent(t, "Proceed", "approval")
	expectIntent(t, "approved", "approval")
	expectIntent(t, "sounds good", "approval")
	expectIntent(t, "yes please", "")
}

func TestPromptFeaturesInstructionBeatsApproval(t *testing.T) {
	expectIntent(t, "looks good, now update the docs", "instruction")
}

func TestPromptFeaturesCorrectionIntent(t *testing.T) {
	expectIntent(t, "No, that's wrong", "correction")
}

func TestPromptFeaturesCorrectionVariants(t *testing.T) {
	expectIntent(t, "Wrong approach", "correction")
	expectIntent(t, "incorrect", "correction")
	expectIntent(t, "that's not what I meant", "correction")
	expectIntent(t, "Don't do that", "correction")
	expectIntent(t, "shouldn't be there", "correction")
	expectIntent(t, "nope", "")
	expectIntent(t, "nothing", "")
}

func TestPromptFeaturesQuestionOpenerMatchesBeforeApostrophe(t *testing.T) {
	// \b falls between "n" and "'", so "can't" opens a question.
	expectIntent(t, "can't do that", "question")
}

func TestPromptFeaturesUnclassifiedPromptHasNullIntent(t *testing.T) {
	expectIntent(t, "hello there", "")
}

func TestPromptFeaturesCaseFoldingIsAsciiOnlyLikeJavaScript(t *testing.T) {
	// U+212A KELVIN SIGN folds to "k" in Go's (?i) but not in a JavaScript /i regexp.
	expectIntent(t, "ma\u212Ae the thing", "")
	// U+017F LATIN SMALL LETTER LONG S likewise folds to "s".
	expectIntent(t, "\u017Fhould we", "")
}

func TestPromptFeaturesApprovalLowercasingMatchesJavaScript(t *testing.T) {
	// JavaScript lowercases U+0130 to "i" plus a combining dot; it does not become a plain "i".
	expectIntent(t, "go for \u0130t", "")
	// U+212A lowercases to "k" in JavaScript, so the approval check still sees "ok".
	expectIntent(t, "O\u212A", "approval")
}

func TestPromptFeaturesLooksLikeTaskStartInstructionSixWordsNoContinuation(t *testing.T) {
	if r := promptFeaturesOf(t, "Fix the authentication bug in login handler"); !r.LooksLikeTaskStart {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesLooksLikeTaskStartFalseWhenFewerThanSixWords(t *testing.T) {
	if r := promptFeaturesOf(t, "Fix this bug"); r.LooksLikeTaskStart {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesLooksLikeTaskStartFalseForContinuationPrefix(t *testing.T) {
	if r := promptFeaturesOf(t, "Also fix the broken test in auth suite"); r.LooksLikeTaskStart {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesLooksLikeTaskStartBoundaryAndContinuationVariants(t *testing.T) {
	if r := promptFeaturesOf(t, "fix one two three four"); r.WordCount != 5 || r.LooksLikeTaskStart {
		t.Errorf("five words: %+v", r)
	}
	if r := promptFeaturesOf(t, "fix one two three four five"); !r.LooksLikeTaskStart {
		t.Errorf("six words: %+v", r)
	}
	for _, p := range []string{"And also fix the broken test now", "now also fix the broken test now", "One more fix the broken test now"} {
		if r := promptFeaturesOf(t, p); r.LooksLikeTaskStart {
			t.Errorf("%q: %+v", p, r)
		}
	}
	if r := promptFeaturesOf(t, "Alsoran: fix the broken test in suite"); !r.LooksLikeTaskStart {
		t.Errorf("continuation needs a word boundary: %+v", r)
	}
	if r := promptFeaturesOf(t, "Why does this fail so often?"); r.LooksLikeTaskStart {
		t.Errorf("question is not a task start: %+v", r)
	}
}

func TestPromptFeaturesWordCountIsCorrect(t *testing.T) {
	if r := promptFeaturesOf(t, "one two three"); r.WordCount != 3 {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesWordCountTrimsAndCollapsesUnicodeWhitespace(t *testing.T) {
	if r := promptFeaturesOf(t, "\u00A0 one \n\t two\u3000three \uFEFF"); r.WordCount != 3 {
		t.Errorf("got %+v", r)
	}
	if r := promptFeaturesOf(t, " \n\t "); r.WordCount != 0 || r.Intent != nil {
		t.Errorf("whitespace only: %+v", r)
	}
}

func TestPromptFeaturesLeadingWhitespaceBeforeSlashIsStillSlashCommand(t *testing.T) {
	r := promptFeaturesOf(t, "  /compact now")
	if !r.IsSlashCommand || r.CommandName == nil || *r.CommandName != "compact" {
		t.Errorf("got %+v", r)
	}
}

func TestPromptFeaturesResultShape(t *testing.T) {
	hookEventTagsMatch(t, promptFeaturesResult{})
	name, intent := "clear", "question"
	j := hookEventJSON(t, promptFeaturesResult{WordCount: 1, IsSlashCommand: true, CommandName: &name, Intent: &intent})
	want := `{"word_count":1,"is_slash_command":true,"command_name":"clear","intent":"question","looks_like_task_start":false}`
	if j != want {
		t.Errorf("got %s", j)
	}
	j = hookEventJSON(t, promptFeaturesResult{})
	want = `{"word_count":0,"is_slash_command":false,"command_name":null,"intent":null,"looks_like_task_start":false}`
	if j != want {
		t.Errorf("got %s", j)
	}
}
