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

// AppDevSkills are the skills a team's dev box gets.
//
//go:embed skills/hi/app
var AppDevSkills embed.FS

// AppDevPageHTML, AppDevPageScript, and AppDevPageStyle are the dev page.
//
//go:embed helper/dev/page.html
var AppDevPageHTML []byte

//go:embed helper/dev/dev.js
var AppDevPageScript []byte

//go:embed helper/dev/dev.css
var AppDevPageStyle []byte

// AdminPageHTML, AdminPageScript, and AdminPageStyle are the admin page
// (hi admin), which loads the dev page's stylesheet too.
//
//go:embed helper/admin/page.html
var AdminPageHTML []byte

//go:embed helper/admin/admin.js
var AdminPageScript []byte

//go:embed helper/admin/admin.css
var AdminPageStyle []byte

// AdminWallHTML and AdminWallScript are a wall screen's page: the org's
// Running and Requests, read-only, from a wall link.
//
//go:embed helper/admin/wall.html
var AdminWallHTML []byte

//go:embed helper/admin/wall.js
var AdminWallScript []byte

// AppXtermScript, AppXtermStyle, and AppXtermFit are the dev page's terminal.
//
//go:embed third_party/xterm/xterm.js
var AppXtermScript []byte

//go:embed third_party/xterm/xterm.css
var AppXtermStyle []byte

//go:embed third_party/xterm/addon-fit.js
var AppXtermFit []byte

// AppDocsPageSource and AppDocsCSS are the app's docs page; AppDocsPele draws its diagrams.
//
//go:embed helper/docs/page.html
var AppDocsPageSource string

//go:embed helper/docs/docs.css
var AppDocsCSS string

//go:embed third_party/pele/pele.js
var AppDocsPele []byte

// AppHelperSource is the helper on the app's pages, with the finder and screenshot scripts it loads.
//
//go:embed helper/helper.js
var AppHelperSource []byte

//go:embed third_party/finder/finder.js
var AppFinderSource []byte

//go:embed third_party/modern-screenshot/modern-screenshot.js
var AppSnapSource []byte

// AppLeadPluginManifest and AppLeadPluginSource are the lead's Hermes plugin; AppLeadAck* is its hook that acknowledges DMs.
//
//go:embed helper/hermes/plugin.yaml
var AppLeadPluginManifest []byte

//go:embed helper/hermes/__init__.py
var AppLeadPluginSource []byte

//go:embed helper/hermes/hooks/dm-ack/HOOK.yaml
var AppLeadAckManifest []byte

//go:embed helper/hermes/hooks/dm-ack/handler.py
var AppLeadAckSource []byte

// BuiltinTemplateFiles are the project templates behind hi init.
//
//go:embed all:templates
var BuiltinTemplateFiles embed.FS
