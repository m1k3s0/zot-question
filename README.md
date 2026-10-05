# zot-question

A Go zot extension that ports the core feature and interaction model of
`npm:@mrclrchtr/supi-ask-user` to zot: an LLM-callable `ask_user` tool that
opens a blocking, keyboard-driven decision form. It requires zot v0.3.73 or
newer, which provides cancellable interactive extension tools.

## Current proposal / implementation

- One focused form containing 1–10 related questions.
- Single-choice, multi-choice, and free-text questions.
- Recommendations are hints, never answers: a choice recommendation
  preselects the option (select it to confirm) and a text recommendation is
  shown as a placeholder. The question stays unanswered until you answer it
  yourself.
- Choice descriptions/details are shown beside the focused option.
- Form, question, and option comments.
- A review screen before submission.
- Escape or closing the panel cancels the tool call.
- Cancelling the agent turn or shutting down zot closes the panel and cancels
  the form.
- No network requests, external process, or persistent answer storage.

The panel uses zot's native extension panel API, so it works without a second
TUI dependency. The `ask_user` tool is registered as an interactive tool, so
zot does not apply its normal 60-second extension-tool timeout while the form
is open. This requires an interactive zot host; headless modes cannot display
the panel. This is intentionally the first slice: unlike Pi's richer custom
component API, zot panels currently provide lines plus key events. The
interaction is therefore equivalent in behavior, while rendering remains
textual.

## Build and try

```sh
git clone ... ../zot-question
cd ../zot-question
go build -o zot-question .
zot --ext .
```

Or install it:

```sh
zot ext install .
```

The model can then call `ask_user` with a payload such as:

```json
{
  "title": "Project setup",
  "intro": "Choose the defaults for this repository.",
  "questions": [
    {
      "id": "package-manager",
      "type": "choice",
      "header": "Packages",
      "prompt": "Which package manager should be used?",
      "recommendation": "bun",
      "options": [
        {"value":"bun", "label":"Bun", "description":"Fast local installs"},
        {"value":"npm", "label":"npm", "description":"Broadest compatibility"}
      ]
    },
    {
      "id": "notes",
      "type": "text",
      "header": "Notes",
      "prompt": "What should the agent keep in mind?",
      "placeholder": "Optional context",
      "recommendation": "Prefer small, reviewable changes."
    }
  ]
}
```

## Controls

- `↑`/`↓`: move through options or review rows
- `Space`: select/toggle an option
- `Enter`: accept the current answer, or submit from review
- `Tab`: advance to the next question
- `c`: comment on the current question
- `n`: comment on the focused choice
- `e`: edit the focused review row; `Esc`: cancel

## Compatibility notes

The package intentionally keeps the same public question vocabulary and limits
as Supi Ask User. The model-visible result is a concise text summary, while
zot's tool result remains the source of truth for the current turn. A later
version can add richer structured result metadata when zot exposes a details
field for extension tool results.
