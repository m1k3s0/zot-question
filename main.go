package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mattn/go-runewidth"
	"github.com/patriceckhart/zot/packages/agent/ext"
	"github.com/patriceckhart/zot/packages/tui"
	"golang.org/x/term"
)

const name = "zot-question"

// version is overridden by release builds with -ldflags; local builds use the
// version declared in extension.json.
var version = "0.1.0"

const schema = `{"type":"object","properties":{"title":{"type":"string","description":"Short decision title"},"intro":{"type":"string","description":"Context shown above the questions"},"questions":{"type":"array","minItems":1,"maxItems":10,"items":{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string","enum":["choice","text"]},"header":{"type":"string"},"prompt":{"type":"string"},"options":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"},"details":{"type":"string"}},"required":["value","label"]}},"multi":{"type":"boolean"},"recommendation":{} ,"placeholder":{"type":"string"}},"required":["id","type","header","prompt"]}}},"required":["questions"]}`

type option struct {
	Value, Label, Description, Details string
	Selected                           bool
	Comment                            string
}
type question struct {
	ID, Type, Header, Prompt, Placeholder, Recommendation string
	Multi, Answered                                       bool
	Options                                               []option
	Text, Comment                                         string
}
type params struct {
	Title, Intro string
	Questions    []struct {
		ID, Type, Header, Prompt, Placeholder string
		Options                               []struct{ Value, Label, Description, Details string }
		Multi                                 bool
		Recommendation                        any `json:"recommendation"`
	}
}

type form struct {
	title, intro string
	questions    []question
	cursor       int
	mode         string // answer, review, comment, option-comment
	comment      string
	option       int
	result       chan ext.ToolResult
	once         sync.Once
	e            *ext.Extension
	panelID      string
}

func main() {
	e := ext.New(name, version)
	e.InteractiveTool("ask_user", "Ask the user one focused decision using a structured keyboard-driven form.", json.RawMessage(schema), func(ctx context.Context, raw json.RawMessage) ext.ToolResult {
		var in params
		if err := json.Unmarshal(raw, &in); err != nil {
			return ext.TextErrorResult("invalid ask_user arguments: " + err.Error())
		}
		f, err := newForm(e, in)
		if err != nil {
			return ext.TextErrorResult(err.Error())
		}
		f.open()
		select {
		case result := <-f.result:
			return result
		case <-ctx.Done():
			f.cancel("ask_user cancelled")
			return ext.TextErrorResult("ask_user cancelled")
		}
	})
	if err := e.Run(); err != nil {
		e.Logf("fatal: %v", err)
		os.Exit(1)
	}
}

func newForm(e *ext.Extension, in params) (*form, error) {
	if len(in.Questions) < 1 || len(in.Questions) > 10 {
		return nil, fmt.Errorf("ask_user supports 1-10 questions (got %d)", len(in.Questions))
	}
	f := &form{e: e, title: strings.TrimSpace(in.Title), intro: strings.TrimSpace(in.Intro), mode: "answer", result: make(chan ext.ToolResult, 1)}
	f.panelID = fmt.Sprintf("ask-user-%p", f)
	seen := map[string]bool{}
	for _, q := range in.Questions {
		q.ID, q.Header, q.Prompt = strings.TrimSpace(q.ID), strings.TrimSpace(q.Header), strings.TrimSpace(q.Prompt)
		if q.ID == "" || q.Header == "" || q.Prompt == "" {
			return nil, fmt.Errorf("each question requires non-empty id, header, and prompt")
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		item := question{ID: q.ID, Type: q.Type, Header: q.Header, Prompt: q.Prompt, Placeholder: strings.TrimSpace(q.Placeholder), Multi: q.Multi}
		switch q.Type {
		case "text":
			if s, ok := q.Recommendation.(string); ok {
				item.Text = s
				item.Recommendation = s
				item.Answered = strings.TrimSpace(s) != ""
			}
		case "choice":
			if len(q.Options) < 2 || len(q.Options) > 12 {
				return nil, fmt.Errorf("choice question %q requires 2-12 options", q.ID)
			}
			for _, o := range q.Options {
				if strings.TrimSpace(o.Value) == "" || strings.TrimSpace(o.Label) == "" {
					return nil, fmt.Errorf("choice question %q has an empty option", q.ID)
				}
				item.Options = append(item.Options, option{Value: strings.TrimSpace(o.Value), Label: strings.TrimSpace(o.Label), Description: o.Description, Details: o.Details})
			}
			if q.Multi {
				if values, ok := q.Recommendation.([]any); ok {
					for _, v := range values {
						f.selectValue(&item, fmt.Sprint(v))
					}
				}
			} else if s, ok := q.Recommendation.(string); ok {
				item.Recommendation = s
				f.selectValue(&item, s)
			}
			if !q.Multi && !hasSelection(item.Options) {
				item.Options[0].Selected = true
			}
			item.Answered = hasSelection(item.Options)
		default:
			return nil, fmt.Errorf("question %q has unsupported type %q", q.ID, q.Type)
		}
		f.questions = append(f.questions, item)
	}
	return f, nil
}

func hasSelection(options []option) bool {
	for _, o := range options {
		if o.Selected {
			return true
		}
	}
	return false
}
func (f *form) selectValue(q *question, value string) {
	for i := range q.Options {
		if q.Options[i].Value == strings.TrimSpace(value) {
			q.Options[i].Selected = true
			return
		}
	}
}
func (f *form) pid() string { return f.panelID }
func (f *form) open() {
	pid := f.pid()
	f.e.OnPanelKey(pid, func(key, text string) { f.key(pid, key, text) }, func() {
		f.cancel("ask_user cancelled: the form was closed")
	})
	f.e.OpenPanel(pid, f.panelTitle(), f.lines(), f.footer())
}
func (f *form) finish(result ext.ToolResult) {
	f.once.Do(func() {
		f.e.ClosePanel(f.pid())
		f.result <- result
	})
}
func (f *form) cancel(message string) { f.finish(ext.TextErrorResult(message)) }
func (f *form) redraw(pid string)     { f.e.RenderPanel(pid, f.panelTitle(), f.lines(), f.footer()) }
func (f *form) key(pid, key, text string) {
	if f.mode == "comment" || f.mode == "option-comment" {
		f.commentKey(pid, key, text)
		return
	}
	if f.mode == "review" {
		f.reviewKey(pid, key, text)
		return
	}
	// zot delivers the spacebar as a rune event (text == " "), not as a
	// named "space" key event. Normalize it before handling answer controls.
	if key == "rune" && text == " " {
		key = "space"
	}
	q := &f.questions[f.cursor]
	switch key {
	case "up":
		f.move(-1)
	case "down":
		f.move(1)
	case "left":
		if f.cursor > 0 {
			f.cursor--
			f.option = 0
		}
	case "right", "tab":
		if f.cursor < len(f.questions)-1 {
			f.cursor++
			f.option = 0
		} else {
			f.mode = "review"
		}
	case "backtab":
		if f.cursor > 0 {
			f.cursor--
		}
	case "space":
		if q.Type == "choice" {
			if q.Multi {
				q.Options[f.option].Selected = !q.Options[f.option].Selected
			} else {
				for i := range q.Options {
					q.Options[i].Selected = i == f.option
				}
			}
			q.Answered = true
		}
	case "enter":
		if len(f.questions) == 1 {
			f.finish(f.resultValue())
			return
		}
		f.next()
	case "backspace":
		if q.Type == "text" {
			r := []rune(q.Text)
			if len(r) > 0 {
				q.Text = string(r[:len(r)-1])
			}
			q.Answered = strings.TrimSpace(q.Text) != ""
		}
	case "rune":
		// Text answers accept every rune, including letters used by answer-mode
		// shortcuts.
		if q.Type == "text" {
			q.Text += text
			q.Answered = strings.TrimSpace(q.Text) != ""
			break
		}
		switch strings.ToLower(text) {
		case "c":
			f.mode = "comment"
			f.comment = q.Comment
		case "n":
			f.mode = "option-comment"
			f.comment = q.Options[f.option].Comment
		}
	}
	f.redraw(pid)
}
func (f *form) move(delta int) {
	q := &f.questions[f.cursor]
	if q.Type == "choice" {
		f.option += delta
		if f.option < 0 {
			f.option = len(q.Options) - 1
		}
		if f.option >= len(q.Options) {
			f.option = 0
		}
	} else if delta != 0 {
		if delta > 0 && f.cursor < len(f.questions)-1 {
			f.cursor++
		}
		if delta < 0 && f.cursor > 0 {
			f.cursor--
		}
	}
}
func (f *form) next() {
	if f.cursor < len(f.questions)-1 {
		f.cursor++
		f.option = 0
	} else {
		f.mode = "review"
	}
}
func (f *form) commentKey(pid, key, text string) {
	switch key {
	case "backspace":
		r := []rune(f.comment)
		if len(r) > 0 {
			f.comment = string(r[:len(r)-1])
		}
	case "enter":
		q := &f.questions[f.cursor]
		if f.mode == "comment" {
			q.Comment = strings.TrimSpace(f.comment)
		} else {
			q.Options[f.option].Comment = strings.TrimSpace(f.comment)
		}
		f.comment = ""
		f.mode = "answer"
	case "esc":
		f.comment = ""
		f.mode = "answer"
	case "rune":
		f.comment += text
	}
	f.redraw(pid)
}
func (f *form) reviewKey(pid, key, text string) {
	switch key {
	case "left":
		f.mode = "answer"
		f.cursor = len(f.questions) - 1
	case "up":
		f.move(-1)
	case "down":
		f.move(1)
	case "enter":
		f.finish(f.resultValue())
		return
	case "rune":
		switch strings.ToLower(text) {
		case "e":
			f.mode = "answer"
			f.cursor = f.cursor % len(f.questions)
		}
	case "esc":
		f.cancel("ask_user cancelled by user")
		return
	}
	f.redraw(pid)
}

func dimText(s string) string { return "\x1b[2m" + s + "\x1b[22m" }

func (f *form) panelTitle() string {
	if f.title != "" {
		return "Ask User — " + f.title
	}
	return "Ask User"
}

// contentWidth is the number of cells one panel row may occupy. zot clips
// panel rows at the terminal width instead of reflowing them and never tells
// an extension how wide its panel is, so the extension measures the terminal
// itself. COLUMNS wins when it is set (an explicit override, and what tests
// pin), the controlling terminal is next, and 80 is the conservative last
// resort. The two-cell panel indent and a little right-side breathing room are
// subtracted here, so callers can treat the result as the full row budget.
func contentWidth() int {
	width := 0
	if columns := os.Getenv("COLUMNS"); columns != "" {
		if n, err := strconv.Atoi(columns); err == nil && n > 0 {
			width = n
		}
	}
	if width == 0 {
		if measured, ok := ttyWidth(); ok {
			width = measured
		}
	}
	if width == 0 {
		width = 80
	}
	width -= 4
	if width < 1 {
		width = 1
	}
	return width
}

// ttyWidth measures the terminal the panel is drawn in. Extension stdin and
// stdout are pipes to zot, so the size has to come from the controlling
// terminal (/dev/tty on unix, CONOUT$ on Windows). Measuring on every redraw
// instead of once lets a terminal resize reflow the panel, which zot does not
// announce to extensions.
func ttyWidth() (int, bool) {
	for _, path := range []string{"/dev/tty", "CONOUT$"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		width, _, err := term.GetSize(int(f.Fd()))
		_ = f.Close()
		if err == nil && width > 0 {
			return width, true
		}
	}
	return 0, false
}

// wrapText folds text into panel rows that fit contentWidth(). first is
// prepended to the first row and every continuation row is indented to the
// same column, so wrapped text stays aligned under its own start. zot clips
// panel rows instead of reflowing them, so anything that can grow with a
// model-written prompt or a user-typed answer has to be wrapped here.
// first must be plain text: it is measured, never wrapped.
func wrapText(first, text string) []string {
	// Tabs and carriage returns are not panel-friendly: the host counts row
	// width in cells and runewidth scores a tab as zero, so expand tabs
	// (over-estimating is safe) and drop CRs.
	text = strings.ReplaceAll(text, "\t", "        ")
	text = strings.ReplaceAll(text, "\r", "")

	firstWidth := runewidth.StringWidth(first)
	limit := contentWidth() - firstWidth
	if limit < 1 {
		limit = 1
	}
	indent := strings.Repeat(" ", firstWidth)

	rows := make([]string, 0, 1)
	for _, line := range strings.Split(text, "\n") {
		for _, part := range tui.WrapANSILine(line, limit) {
			if len(rows) == 0 {
				rows = append(rows, first+part)
				continue
			}
			rows = append(rows, indent+part)
		}
	}
	return rows
}

// introLines renders the preamble with the same Markdown subset used by zot's
// transcript and wraps the resulting ANSI text to the available panel width.
// Panel lines are otherwise opaque to the host: passing the whole intro as one
// string makes a long preamble get clipped instead of reflowing.
func (f *form) introLines() []string {
	width := contentWidth()
	rendered := tui.RenderMarkdown(f.intro, tui.Dark, width)
	result := make([]string, 0, strings.Count(rendered, "\n")+1)
	for _, line := range strings.Split(rendered, "\n") {
		for _, part := range tui.WrapANSILine(line, width) {
			result = append(result, "  "+part)
		}
	}
	return append(result, "")
}

func (f *form) lines() []string {
	if f.mode == "review" {
		return f.reviewLines()
	}
	q := f.questions[f.cursor]
	lines := []string{f.breadcrumbs(), ""}
	lines = append(lines, wrapText("  ", q.Prompt)...)
	lines = append(lines, "")
	if f.intro != "" && f.cursor == 0 {
		lines = append(f.introLines(), lines...)
	}

	if q.Type == "text" {
		value := q.Text
		if value == "" && q.Placeholder != "" {
			value = "[" + q.Placeholder + "]"
		}
		// The cursor rides along in the wrapped text, so a full last row
		// pushes it onto a new row instead of clipping it.
		lines = append(lines, wrapText("  ", value+"▌")...)
	} else {
		for i, o := range q.Options {
			mark := "○"
			if o.Selected {
				mark = "●"
			}
			if q.Multi {
				mark = "☑"
				if !o.Selected {
					mark = "☐"
				}
			}
			cursor := "  "
			if i == f.option {
				cursor = "› "
			}
			lines = append(lines, wrapText(cursor+mark+" ", o.Label)...)
			if i == f.option && (o.Description != "" || o.Details != "") {
				lines = append(lines, wrapText("    ", o.Description)...)
				lines = append(lines, wrapText("    ", o.Details)...)
			}
		}
	}
	if f.mode == "comment" {
		lines = append(lines, "")
		lines = append(lines, wrapText("  Comment: ", f.comment+"▌")...)
	} else if f.mode == "option-comment" {
		lines = append(lines, "")
		lines = append(lines, wrapText("  Option comment: ", f.comment+"▌")...)
	} else if q.Comment != "" {
		lines = append(lines, "")
		lines = append(lines, wrapText("  Comment: ", q.Comment)...)
	}
	return lines
}
func (f *form) breadcrumbs() string {
	plainSteps := make([]string, 0, len(f.questions))
	for i, q := range f.questions {
		mark := "○"
		if q.Answered {
			mark = "✓"
		}
		plainSteps = append(plainSteps, fmt.Sprintf("%d %s %s", i+1, mark, q.Header))
	}

	// Keep the stepper on one line only when the complete, unstyled content
	// fits; otherwise each step gets its own row. Re-measuring, rather than
	// caching, lets a terminal resize naturally switch layouts.
	if stepWidth := runewidth.StringWidth("  " + strings.Join(plainSteps, "   ")); stepWidth <= contentWidth() {
		steps := make([]string, 0, len(f.questions))
		for i, step := range plainSteps {
			steps = append(steps, f.styleStep(i, step))
		}
		// Keep the line indented so a leading step number is not interpreted as
		// an ordered-list marker by the panel renderer.
		return "  " + strings.Join(steps, "   ")
	}

	steps := make([]string, 0, len(f.questions))
	for i, step := range plainSteps {
		steps = append(steps, "  "+f.styleStep(i, step))
	}
	return strings.Join(steps, "\n")
}

func (f *form) styleStep(index int, step string) string {
	if index == f.cursor {
		parts := strings.SplitN(step, " ", 3)
		if len(parts) == 3 {
			step = parts[0] + " ● " + parts[2]
		}
		return "\x1b[1;36m" + step + "\x1b[0m"
	}
	if f.questions[index].Answered {
		return "\x1b[32m" + step + "\x1b[0m"
	}
	return dimText(step)
}

func (f *form) reviewLines() []string {
	lines := []string{"  Review your answers", "", f.breadcrumbs(), ""}
	if f.intro != "" {
		lines = append(lines, f.introLines()...)
	}
	for i, q := range f.questions {
		mark := "✓"
		cursor := "  "
		if i == f.cursor {
			cursor = "› "
		}
		lines = append(lines, wrapText(fmt.Sprintf("%s%s %s: ", cursor, mark, q.Header), f.answerText(q))...)
		if q.Comment != "" {
			for _, row := range wrapText("    ", "Comment: "+q.Comment) {
				lines = append(lines, dimText(row))
			}
		}
	}
	if f.comment != "" {
		lines = append(lines, "")
		lines = append(lines, wrapText("  Form comment: ", f.comment)...)
	}
	return lines
}
func (f *form) answerText(q question) string {
	if q.Type == "text" {
		return q.Text
	}
	var a []string
	for _, o := range q.Options {
		if o.Selected {
			a = append(a, o.Label)
		}
	}
	return strings.Join(a, ", ")
}
func (f *form) footer() string {
	if f.mode == "review" {
		return "↑/↓ review · e edit · enter submit · esc cancel"
	}
	if f.mode == "comment" || f.mode == "option-comment" {
		return "type comment · enter save · esc cancel"
	}
	if len(f.questions) == 1 {
		return "↑/↓ move · space select · enter submit · c comment · esc cancel"
	}
	return "↑/↓ move · space select · enter next · tab next · c comment · esc cancel"
}

func (f *form) resultValue() ext.ToolResult {
	outcome := "submitted"
	for _, q := range f.questions {
		if !q.Answered {
			outcome = "needs_discussion"
		}
	}
	var b strings.Builder
	if outcome == "needs_discussion" {
		b.WriteString("User needs discussion before a complete decision.\n")
	}
	for _, q := range f.questions {
		if q.Answered {
			fmt.Fprintf(&b, "%s: %s\n", q.Header, f.answerText(q))
		} else {
			fmt.Fprintf(&b, "%s: unanswered\n", q.Header)
		}
		if q.Comment != "" {
			fmt.Fprintf(&b, "%s comment: %s\n", q.Header, q.Comment)
		}
		for _, o := range q.Options {
			if o.Comment != "" {
				fmt.Fprintf(&b, "%s option comment (%s): %s\n", q.Header, o.Label, o.Comment)
			}
		}
	}
	return ext.TextResult(b.String())
}
