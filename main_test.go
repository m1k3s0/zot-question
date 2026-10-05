package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestPanelLinesAreSinglePhysicalRows(t *testing.T) {
	// The host renders each element of the panel lines as exactly one terminal
	// row and never splits embedded newlines. Returning the stepper as a single
	// string with embedded "\n" corrupts the diff renderer's cursor accounting
	// and paints the steps as a diagonal staircase. Force the multi-line
	// stepper layout with a narrow terminal.
	t.Setenv("COLUMNS", "40")
	f := &form{questions: []question{
		{ID: "a", Type: "choice", Header: "Beverage", Options: []option{{Value: "coffee", Label: "Coffee"}}},
		{ID: "b", Type: "choice", Header: "A Header That Is Far Too Long To Share A Row", Options: []option{{Value: "x", Label: "X"}}},
		{ID: "c", Type: "choice", Header: "Toppings", Options: []option{{Value: "y", Label: "Y"}}},
	}}

	assertSingleRows(t, "answer", f.lines())
	if got := len(f.breadcrumbLines()); got < len(f.questions) {
		t.Fatalf("expected one breadcrumb row per question, got %d", got)
	}

	f.mode = "review"
	assertSingleRows(t, "review", f.reviewLines())
}

func assertSingleRows(t *testing.T, mode string, lines []string) {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, "\n") {
			t.Errorf("%s: panel line %d contains an embedded newline: %q", mode, i, line)
		}
	}
}

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
