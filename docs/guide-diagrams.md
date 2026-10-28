# Structure diagrams in the guide

Every hi tool's main guide page shows how its parts fit together in a
diagram, near the top, right after the opening paragraphs: what runs where,
what sits inside what, and which way things go. People grasp a tool's shape
faster from a picture than from paragraphs, and the picture tells them what
the rest of the page is about. The idea comes from how
[OpenRig](https://openrig.dev/rigs) draws its teams: boxes inside boxes,
numbered marks, and a key that explains each mark.

## The rule

- A tool's main page has a diagram. `guide_diagrams_test.go` lists the
  pages that must have one (`guideDiagramPages`); a new tool's page goes
  there with its diagram.
- A change to how a tool's parts connect changes its diagram in the same
  commit, like the guide's text.
- `go test -run TestGuide` checks every diagram: its fields, its colours,
  that each mark is in the key, and that each diagram is on a page.

## Adding one

A diagram is a JSON file in `_data/diagrams/`, drawn by
`_includes/diagram.html`. In the page:

```liquid
{% include diagram.html name="box" %}
```

```jsonc
{
  "caption": "How hi box works. On your machine, hi box starts ...",  // the whole diagram in sentences
  "you": "You",                          // who starts it, on top (optional)
  "via": "hi box shell",                 // the command or channel they use
  "frame": "your machine",               // where it all runs
  "frame_mark": 1,
  "groups": [                            // the parts inside the frame, 1 to 4, in the order things happen
    {
      "name": "box", "label": "rootless container", "mark": 3,
      "via": "mounted",                  // the arrow into this group
      "summary": "One line on the part.",
      "items": [                         // cards
        {"title": "myproject-1",         // what people see: a name, a path, an address
         "role": "shell, command, or agent",
         "does": "One sentence; `code` is fine.",
         "chip": "Podman", "tone": "accent",
         "entry": "You work here"}       // marks where people come in (optional)
      ]
    }
  ],
  "after": [ /* parts outside the frame, below it, each with its own "via" arrow */ ],
  "key": [ {"mark": 1, "term": "Your machine", "text": "Where hi runs ..."} ]
}
```

## Writing it

- **Use the guide's words**, and titles as people see them on screen: a
  box's name, a Slack handle, a command, a path. Invent nothing that isn't
  in hi.
- **Inside or outside the frame is a claim.** Put a part in the frame only
  if it really runs there (Slack isn't in your NetBird network).
- **Arrows read in the order things happen**, labelled with what crosses
  them: a command, "every connection", "merged".
- **Number what readers need to learn**, usually the frame and each group,
  and explain each number in the key in a sentence. Marks run 1, 2, 3.
- **Keep it small:** at most four groups in the frame, a sentence per card.
  Four groups fit side by side in the guide's column; on a phone they stack.
- **The caption is the text alternative:** screen readers and search get
  it instead of the boxes, so it says everything the diagram shows.
- **Angle brackets** in `does`, `summary`, and key texts go in backticks
  (`` `hi-box/<name>` ``); plain-text fields are escaped.
- **Tones** colour the chip's dot: `accent` for hi itself, `mint` for the
  network, links, and what is allowed, `amber` for Claude Code, `sky` for
  Codex, `violet` for Hermes and Slack, `red` for danger, `dim` for plain
  things and the outside world.

## Steps

A workflow that changes things over time, rather than a structure, is
drawn step by step: `_data/steps/<name>.json`, drawn by
`_includes/steps.html`, with `{% include steps.html name="team-data" %}`.
Each step has a title, an optional command, the two places it changes side
by side (`left` and `right`, each a `name` and its `folders` of `files`,
every file with a `tag` and a tone), a `via` for the arrow between them
(`back` turns it around; `"arrow": false` leaves only the words, for
"nothing crosses"), and a sentence or two of `text`. A `legend`
says what the tones mean. The caption, again, says it all in sentences;
`go test -run TestGuideSteps` checks the fields and that a page shows it.
[Preview and production data](/guide/team/data/) is the first.

## Seeing it

GitHub Pages builds the guide with Jekyll 3.10. To see a diagram before
pushing, build with the same version and look at it at desktop, tablet, and
phone widths:

```sh
podman run --rm --userns=keep-id -v $PWD:/srv/jekyll:ro,Z -v /tmp/site:/out:Z \
  docker.io/jekyll/jekyll:pages jekyll build --source /srv/jekyll --destination /out
python3 -m http.server --directory /tmp/site 8765 &
chrome-headless-shell --hide-scrollbars --window-size=1440,2400 \
  --screenshot=box.png http://127.0.0.1:8765/guide/box/    # then 1000 and 390 wide
```
