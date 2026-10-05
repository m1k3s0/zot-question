package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestIntroLinesRenderMarkdownAndWrap(t *testing.T) {
	t.Setenv("COLUMNS", "32")
	f := &form{intro: "# Context\n\nChoose **carefully** from these options.\n\n- first\n- second"}

	lines := f.introLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Context") {
		t.Fatalf("intro heading was not rendered: %q", joined)
	}
	if !strings.Contains(joined, "carefully") {
		t.Fatalf("intro body was not rendered: %q", joined)
	}
	if strings.Contains(joined, "**carefully**") {
		t.Fatalf("raw Markdown leaked into intro: %q", joined)
	}
	if len(lines) < 6 {
		t.Fatalf("expected Markdown paragraphs and list to occupy multiple lines, got %d: %q", len(lines), joined)
	}
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for i, line := range lines {
		if len([]rune(ansi.ReplaceAllString(line, ""))) > 32 {
			t.Errorf("line %d exceeds terminal width: %d runes: %q", i, len([]rune(ansi.ReplaceAllString(line, ""))), line)
		}
	}
}

func TestRecommendationsNeverCountAsAnswers(t *testing.T) {
	// A recommendation is a hint: it may preselect an option or show as a
	// placeholder, but the question must stay unanswered until the user acts.
	const raw = `{"questions":[
		{"id":"a","type":"choice","header":"A","prompt":"Pick","options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}]},
		{"id":"b","type":"choice","header":"B","prompt":"Pick","options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}],"recommendation":"y"},
		{"id":"c","type":"choice","header":"C","prompt":"Pick","multi":true,"options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}]},
		{"id":"d","type":"choice","header":"D","prompt":"Pick","multi":true,"options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}],"recommendation":["y"]},
		{"id":"e","type":"text","header":"E","prompt":"Type","recommendation":"suggested answer"}
	]}`
	var in params
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	f, err := newForm(nil, in)
	if err != nil {
		t.Fatal(err)
	}

	for _, q := range f.questions {
		if q.Answered {
			t.Errorf("question %q should start unanswered", q.ID)
		}
	}

	// No recommendation: nothing preselected.
	if hasSelection(f.questions[0].Options) {
		t.Errorf("question a should not preselect an option")
	}
	// Choice recommendation preselects and marks the option, but does not
	// answer the question.
	if !f.questions[1].Options[1].Selected || !f.questions[1].Options[1].Recommended {
		t.Errorf("question b should preselect and mark the recommended option")
	}
	if got := f.initialOption(1); got != 1 {
		t.Errorf("cursor should focus the recommended option when question b is active, got %d", got)
	}
	// Multi recommendation likewise.
	if !f.questions[3].Options[1].Selected || !f.questions[3].Options[1].Recommended {
		t.Errorf("question d should preselect and mark the recommended option")
	}
	// Text recommendation is a hint, not a value.
	if f.questions[4].Text != "" {
		t.Errorf("text recommendation should not prefill the field, got %q", f.questions[4].Text)
	}
	if f.questions[4].Recommendation != "suggested answer" {
		t.Errorf("text recommendation should be retained as a hint")
	}
}

func TestTextRecommendationRendersAsHint(t *testing.T) {
	const raw = `{"questions":[{"id":"a","type":"text","header":"A","prompt":"Type","recommendation":"suggested answer"}]}`
	var in params
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	f, err := newForm(nil, in)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.lines(), "\n")
	if !strings.Contains(joined, "[suggested answer]") {
		t.Errorf("text recommendation should render as a placeholder hint: %q", joined)
	}
}

func TestOnlyUserSelectionMarksAnswered(t *testing.T) {
	const raw = `{"questions":[
		{"id":"a","type":"choice","header":"A","prompt":"Pick","options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}],"recommendation":"y"},
		{"id":"b","type":"choice","header":"B","prompt":"Pick","multi":true,"options":[{"value":"x","label":"X"},{"value":"y","label":"Y"}]}
	]}`
	var in params
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	f, err := newForm(nil, in)
	if err != nil {
		t.Fatal(err)
	}

	// Accepting the recommendation by selecting it marks the question answered.
	f.selectOption(&f.questions[0])
	if !f.questions[0].Answered || !f.questions[0].Options[1].Selected {
		t.Errorf("selecting the recommended option should mark question a answered")
	}

	// A multi-select question is only answered while something is selected.
	f.cursor, f.option = 1, 0
	f.selectOption(&f.questions[1])
	if !f.questions[1].Answered {
		t.Errorf("question b should be answered after selecting an option")
	}
	f.selectOption(&f.questions[1])
	if f.questions[1].Answered {
		t.Errorf("question b should return to unanswered after deselecting the last option")
	}
}
