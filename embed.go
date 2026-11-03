// Package hi holds the files the hi binary carries: templates, skills, the
// team pages' scripts, recipes, and bundles. Go embeds files only from a
// package's own folder and below, so they are embedded here, at the root,
// and internal/cli uses them.
package hi

import "embed"

// BuiltinBundleFiles are the bundles hi agent --bundle takes.
//
//go:embed bundles/*.json
var BuiltinBundleFiles embed.FS

// ServeLlamaCppScript builds and starts llama.cpp on a rented machine.
//
//go:embed scripts/compute/serve-llama-cpp.sh
var ServeLlamaCppScript []byte

// SetupScript is the workstation installer behind hi install.
//
//go:embed scripts/install.sh
var SetupScript []byte

// BuiltinRecipeFiles are hi serve's tested recipes.
//
//go:embed recipes
var BuiltinRecipeFiles embed.FS

// HiSkill is the skill that teaches agents to use hi.
//
//go:embed skills/hi/SKILL.md
var HiSkill []byte

// TeamDevSkills are the skills a team's dev box gets.
//
//go:embed skills/hi/team
var TeamDevSkills embed.FS

// TeamDevPageHTML, TeamDevPageScript, and TeamDevPageStyle are the dev page.
//
//go:embed helper/dev/page.html
var TeamDevPageHTML []byte

//go:embed helper/dev/dev.js
var TeamDevPageScript []byte

//go:embed helper/dev/dev.css
var TeamDevPageStyle []byte

// TeamXtermScript, TeamXtermStyle, and TeamXtermFit are the dev page's terminal.
//
//go:embed third_party/xterm/xterm.js
var TeamXtermScript []byte

//go:embed third_party/xterm/xterm.css
var TeamXtermStyle []byte

//go:embed third_party/xterm/addon-fit.js
var TeamXtermFit []byte

// TeamDocsPageSource and TeamDocsCSS are the app's docs page; TeamDocsPele draws its diagrams.
//
//go:embed helper/docs/page.html
var TeamDocsPageSource string

//go:embed helper/docs/docs.css
var TeamDocsCSS string

//go:embed third_party/pele/pele.js
var TeamDocsPele []byte

// TeamHelperSource is the helper on the app's pages, with the finder and screenshot scripts it loads.
//
//go:embed helper/helper.js
var TeamHelperSource []byte

//go:embed third_party/finder/finder.js
var TeamFinderSource []byte

//go:embed third_party/modern-screenshot/modern-screenshot.js
var TeamSnapSource []byte

// TeamLeadPluginManifest and TeamLeadPluginSource are the lead's Hermes plugin; TeamLeadAck* is its hook that acknowledges DMs.
//
//go:embed helper/hermes/plugin.yaml
var TeamLeadPluginManifest []byte

//go:embed helper/hermes/__init__.py
var TeamLeadPluginSource []byte

//go:embed helper/hermes/hooks/dm-ack/HOOK.yaml
var TeamLeadAckManifest []byte

//go:embed helper/hermes/hooks/dm-ack/handler.py
var TeamLeadAckSource []byte

// BuiltinTemplateFiles are the project templates behind hi init.
//
//go:embed all:templates
var BuiltinTemplateFiles embed.FS
