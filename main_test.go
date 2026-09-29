package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain drops ANSI styling so tests can reason about what the user sees.
func plain(line string) string { return ansiRE.ReplaceAllString(line, "") }

// maxRowWidth is the widest row the form may emit: the panel budget plus the
// two-cell indent every row carries. Anything wider is clipped by zot at the
// terminal width.
func maxRowWidth() int { return contentWidth() + 2 }

func checkWidths(t *testing.T, lines []string) {
	t.Helper()
	for i, line := range lines {
		if width := runewidth.StringWidth(plain(line)); width > maxRowWidth() {
			t.Errorf("line %d is %d cells wide (budget %d): %q", i, width, maxRowWidth(), plain(line))
		}
	}
}

// flattened joins the panel into one whitespace-normalised string. Wrapping
// replaces spaces with row breaks, so collapsing whitespace back into single
// spaces proves that nothing was dropped.
func flattened(lines []string) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, plain(line))
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func condensed(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func textForm(prompt string) *form {
	return &form{questions: []question{{ID: "q", Type: "text", Header: "Answer", Prompt: prompt}}}
}

// COLUMNS is pinned by every test: it is the explicit width override the form
// reads first, so it keeps the expectations independent of the terminal that
// happens to run the test binary.
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
	checkWidths(t, lines)
}

func TestLongPromptWrapsToPanelWidth(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	prompt := "Explain which deployment strategy fits this migration and why the staged rollout is preferable for the service."
	f := textForm(prompt)

	lines := f.lines()
	checkWidths(t, lines)

	if got := flattened(lines); !strings.Contains(got, condensed(prompt)) {
		t.Fatalf("wrapped prompt lost text:\n%s", got)
	}
	for _, line := range lines {
		if strings.Contains(plain(line), prompt) {
			t.Fatalf("prompt was not wrapped: %q", plain(line))
		}
	}
}

func TestTypedCommentWrapsAndKeepsCursor(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	comment := "Stage the rollout behind a feature flag first because the schema change is not backwards compatible."
	f := &form{
		mode:    "comment",
		comment: comment,
		questions: []question{{
			ID: "q", Type: "choice", Header: "Rollout", Prompt: "Pick one",
			Options: []option{{Value: "canary", Label: "Canary"}, {Value: "big-bang", Label: "Big bang"}},
		}},
	}

	lines := f.lines()
	checkWidths(t, lines)

	if last := plain(lines[len(lines)-1]); !strings.HasSuffix(last, "▌") {
		t.Fatalf("cursor is not on the last comment row: %q", last)
	}
	if got := flattened(lines); !strings.Contains(got, condensed(comment)) {
		t.Fatalf("wrapped comment lost text:\n%s", got)
	}
}

func TestLongOptionRowsWrap(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	label := "Canary the change behind a feature flag and widen the blast radius slowly"
	description := "Keeps the rollback cheap because the new code path stays off for most sessions."
	details := "Requires a flag service, an on-call owner, and a metric with an alert before the first session sees it."
	f := &form{questions: []question{{
		ID: "q", Type: "choice", Header: "Rollout", Prompt: "Pick one",
		Options: []option{
			{Value: "canary", Label: label, Description: description, Details: details},
			{Value: "big-bang", Label: "Big bang"},
		},
	}}}

	lines := f.lines()
	checkWidths(t, lines)

	got := flattened(lines)
	for _, want := range []string{label, description, details} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped option row lost text %q:\n%s", want, got)
		}
	}
}

func TestReviewLinesWrap(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	answer := "Stage the rollout behind a feature flag and widen the blast radius slowly over the next two weeks"
	comment := "The flag owner is the platform team, so schedule the switch inside their on-call window."
	f := &form{
		mode: "review",
		questions: []question{{
			ID: "q", Type: "text", Header: "Rollout", Prompt: "Pick one",
			Text: answer, Answered: true, Comment: comment,
		}},
	}

	lines := f.lines()
	checkWidths(t, lines)

	got := flattened(lines)
	for _, want := range []string{answer, comment} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped review row lost text %q:\n%s", want, got)
		}
	}
}

func TestWrapTextHangsIndentAndSplitsLongTokens(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	first := "  Comment: "
	rows := wrapText(first, "short\n"+strings.Repeat("x", 100))

	if len(rows) < 5 {
		t.Fatalf("expected the comment to occupy multiple rows, got %d: %q", len(rows), rows)
	}
	if rows[0] != first+"short" {
		t.Fatalf("first row = %q, want %q", rows[0], first+"short")
	}
	indent := strings.Repeat(" ", runewidth.StringWidth(first))
	for i, row := range rows[1:] {
		if !strings.HasPrefix(row, indent) || strings.HasPrefix(row, indent+" ") {
			t.Fatalf("row %d is not hang-indented under %q: %q", i+1, first, row)
		}
	}
	if kept := strings.Count(strings.Join(rows, ""), "x"); kept != 100 {
		t.Fatalf("long token lost characters: kept %d of 100", kept)
	}
	checkWidths(t, rows)
}

// wrapText is the only defence against clipping: test the row budget it
// promises rather than the rendering of any single call site.
func TestWrapTextRespectsPanelBudget(t *testing.T) {
	t.Setenv("COLUMNS", "40")
	for _, text := range []string{
		"",
		"one",
		"two\n\nlines",
		strings.Repeat("word ", 40),
		strings.Repeat("z", 200),
		"tab\tseparated\tcells",
	} {
		rows := wrapText("    ", text)
		if len(rows) == 0 {
			t.Fatalf("wrapText(%q) returned no rows", text)
		}
		checkWidths(t, rows)
	}
}
