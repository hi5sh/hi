# `hi box --desktop` specification

Status: Draft

Dependencies: `hi box` and its egress proxy
([hi_box.md](../approved/hi_box.md)), bundles
([hi_agent_bundles.md](hi_agent_bundles.md)), and for remote use, box URLs
and `--remote` (hi_box.md releases 3 and 4).

## Goal

Give a box a Linux desktop that an agent works in and a person can watch
and take over, so agents can use apps that have no command line, such as
Blender, FreeCAD, KiCad, or a website that only works in a real browser.
This is the "live desktop view that a person can watch and take over from
an agent" from the roadmap's study of agent-machine services.

Three uses:

- **An agent in a GUI app.** The agent sees the screen in screenshots and
  clicks and types.
- **A person watching.** In a browser, live, without stopping the agent.
- **A person helping.** The agent asks for help, or the person sees it
  stuck; the person takes the mouse, does the step, and hands control back.

Not in this spec: Windows or macOS apps (SolidWorks, Fusion), the host's own
desktop (the bundles spec leaves it out too), and sound.

## Script first, desktop second

Blender and the open CAD tools can be scripted, and scripts are faster,
more reliable, and repeatable where clicks are not:

| App      | Without a display                                         |
|----------|-----------------------------------------------------------|
| Blender  | `blender -b file.blend --python script.py`, the whole `bpy` API; renders with `-f` |
| FreeCAD  | `freecadcmd` and its Python modules                       |
| CadQuery, build123d, OpenSCAD | Code is the model; export STEP, STL, PNG |
| KiCad    | `kicad-cli` and the `pcbnew` Python module                |

The `desktop` skill tells the agent to change models through these, render
a preview image and open it to check its work, and use the desktop only for
what has no API, or when a person asks to watch in the app. The desktop is
mainly for the person.

## Commands

```text
hi box shell --desktop                 a box with a desktop
hi agent --desktop "<task>"            an agent in a box with a desktop
hi box desktop <name>                  open the box's desktop in the browser, view only
hi box desktop <name> --control        open it ready to take over
hi box desktop <name> --url            print the URLs instead of opening them
```

`--desktop` takes an optional size, `--desktop 2560x1440`; the default is
1920x1080. Briefs take `desktop: true` in their front matter, and
`devcontainer.json` takes `customizations.hi.desktop`. `hi box ls` shows a
`desktop` column.

## Design

### The image

`--desktop` adds a layer to the box's image (the base image, or the bundle
image `hi-agent:<hash>`), built once and tagged by its hash as bundles are:

- `tigervnc-standalone-server`: Xvnc, an X server and VNC server in one
  process, with no GPU and no real display.
- `openbox`, a window manager that needs no configuration, and `tint2` for
  a task bar a person can find windows with.
- `novnc` and `python3-websockify`: the browser client.
- `xdotool`, `scrot`, `wmctrl`, `xclip`: input, screenshots, windows, and
  the clipboard for the agent's tool.
- Fonts (`fonts-dejavu`, `fonts-noto-core`) so apps don't draw boxes.

Everything is from Ubuntu 24.04's archive. A browser comes from the `web`
bundle's Chrome, which already runs in boxes; Ubuntu's `chromium` is a
snap and doesn't run in a container. The apps themselves come from a
`cad` bundle (release 2), not this layer.

### What runs in the box

hi's entrypoint starts, as the box's user, before the box's command:

1. `Xvnc :1 -geometry 1920x1080 -depth 24 -localhost no -rfbport 5901
   -PasswordFile ~/.hi/desktop/passwd -SecurityTypes VncAuth`, with two
   passwords in the file: a full one and a view-only one (`vncpasswd`
   supports both). hi makes both at random per box and keeps them in the
   box's state folder, not in the box's home folder.
2. `openbox` and `tint2` on `DISPLAY=:1`.
3. `websockify --web /usr/share/novnc 6080 localhost:5901`.

`DISPLAY=:1` is set for the box's command, so an app the agent starts from
its shell opens on the desktop. Chrome gets the egress proxy as
`--proxy-server`, as the `web` bundle does, so a person browsing in the box
is held to the box's network allowlist; `hi box allow` widens it.

### Reaching it

The box sits on an `--internal` network, so its ports can't be published
directly. The proxy container is on both networks already; it gets a third
listener that forwards to the box's port 6080, published on the host at
`127.0.0.1:<random port>` and recorded in `box.json`. `hi box desktop` opens
`http://127.0.0.1:<port>/vnc.html?autoconnect=1&password=<view-only>`;
`--control` uses the full password. The password in the URL never leaves
the machine, but it is in the browser's history; a short-lived token from
the proxy instead is an open question.

From another machine, release 3 adds the desktop to `hi box url` on
NetBird, and with `--remote` the same listener runs on the rented machine,
reached through the SSH tunnel `hi compute` already makes.

### The agent's tool

A small command in the box, `hi-desktop`, mounted from the hi binary as
the proxy is, so agents need no MCP server and Claude Code, Codex, and
Hermes all use it the same way:

```text
hi-desktop screenshot [--window <name>]   saves a PNG and prints its path
hi-desktop click <x> <y> [--right|--double]
hi-desktop drag <x1> <y1> <x2> <y2>
hi-desktop type "<text>"
hi-desktop key ctrl+s
hi-desktop scroll <x> <y> <up|down> [n]
hi-desktop windows                         open windows, their titles and positions
hi-desktop focus <name>
hi-desktop ask "<what you need from a person>"
hi-desktop wait                            block until a person hands control back
```

The agent opens the PNG with its own image reading, as it does for the
`web` bundle's screenshots today. Screenshots are scaled to at most 1568
pixels on the long side, with the scale printed, and `click` takes
coordinates in the screenshot's pixels, so the agent never converts them.

The `desktop` skill, in the box's home with the `hi` skill, covers script
first, screenshot after every few actions, keyboard shortcuts over menus,
and when to ask.

### Taking over and handing back

One person and one agent on the same desktop would fight over the mouse.
hi keeps a lock in the box's state folder, `desktop.lock`, holding who has
control:

- **The agent has it** by default. A person with the full password can
  still move the mouse; noVNC's page from hi has a bar at the top: *"The
  agent is using this desktop. [Take over]"*.
- **Take over** writes the lock. From then on every `hi-desktop` action but
  `screenshot`, `windows`, and `wait` refuses with *"A person has taken over
  the desktop. Run hi-desktop wait, then take a screenshot."* The agent
  waits instead of guessing.
- **Hand back** clears the lock, with an optional note that `hi-desktop
  wait` prints, such as *"Logged in; the license dialog is closed."*
- **`hi-desktop ask`** writes the request to the box's log, shows it in the
  bar, and with a linked Slack account messages the owner with the
  `hi box desktop <name> --control` command. It returns at once; the agent
  then calls `wait`.

The bar is a page hi serves through the proxy's listener around noVNC's
`vnc.html`; noVNC stays unmodified.

### GPU

Xvnc draws with Mesa's llvmpipe on the CPU. That is enough for apps, a
browser, and Blender with light scenes; the Strix Halo's 16 cores help.
Rendering doesn't need the display: with `--gpu`, Blender's Cycles uses the
GPU through HIP, headless or from the desktop, since the box already gets
`/dev/kfd` and `/dev/dri`.

A GPU-drawn desktop is release 3, from one of:

- **Sway headless and wayvnc.** wlroots' headless backend draws with the
  GPU through EGL and `/dev/dri/renderD128`; Blender 4 runs on Wayland
  natively, other apps through Xwayland. The agent's tool would use
  `grim` and `wtype`/`ydotool` instead of `scrot` and `xdotool`.
- **VirtualGL** around one app on Xvnc: `vglrun blender` draws on the GPU
  and copies frames to the X server. Unverified with ROCm's Mesa on gfx1151.

### Logs

Every `hi-desktop` action goes to `desktop.log` in the box's state folder
with a time and, for clicks and keys, the screenshot the agent last saw.
Takeovers and hand-backs, with who did them, go there too, and with a
server to the audit log. `hi box diff` lists the screenshots with the
changed files.

## Releases

1. **The desktop.** `--desktop` on `hi box` and `hi agent`, the image
   layer, `hi box desktop` on this machine, `hi-desktop`, the `desktop`
   skill, take over and hand back, `desktop.log`.
2. **A `cad` bundle.** Blender from blender.org's Linux tarball, pinned and
   checked against its SHA-256, since Ubuntu's is 4.0; FreeCAD, CadQuery,
   build123d, OpenSCAD, and KiCad, each with what it needs and a skill for
   scripting it.
3. **Away from this machine and on the GPU.** The desktop on `hi box url`
   and `--remote`, a GPU-drawn desktop, Slack messages from
   `hi-desktop ask`, and takeovers in the audit log.

## Risks

- **The desktop is a way in.** It listens on 127.0.0.1 only, behind a
  random password per box; NetBird and approval guard it in release 3.
  VNC's own authentication is weak (DES, 8 characters), which is why it
  never listens beyond the proxy.
- **The clipboard is a way out the proxy doesn't see.** A person copying
  from the box takes data out on purpose; the agent can't reach the host's
  clipboard. noVNC's clipboard stays on for `--control` only.
- **A person can type secrets into the box.** A password typed during a
  takeover is on the screen and in the agent's next screenshot, and
  screenshots are kept in `desktop.log`. The bar says so on takeover, and
  hand-back offers to delete the screenshots taken while a person had
  control.
- **Screen actions go wrong silently.** A click at the wrong place does
  something else; script first is the defence, with checkpoints from
  hi_box.md release 3 before risky GUI work.

## Open questions

1. A short-lived token from the proxy instead of the password in the URL.
2. Whether the agent should get a desktop only when a skill asks for it,
   so `hi agent --bundle cad` turns it on without `--desktop`.
3. Whether one box may have several desktops, for an agent and a person
   working side by side.
4. Whether Sway headless with `/dev/dri` works in a rootless box on
   gfx1151, and how Blender's viewport performs there against llvmpipe.
5. Recording the desktop as video for long runs, against screenshots only.
6. Apps with license servers (some CAD plugins): the box's allowlist and a
   fixed machine ID, since every box has a new one.

## Sources

- TigerVNC Xvnc and vncpasswd (view-only passwords): https://tigervnc.org/doc/Xvnc.html,
  https://tigervnc.org/doc/vncpasswd.html
- noVNC and websockify: https://github.com/novnc/noVNC,
  https://github.com/novnc/websockify
- Blender command line: https://docs.blender.org/manual/en/latest/advanced/command_line/arguments.html
- Blender Cycles on AMD (HIP): https://docs.blender.org/manual/en/latest/render/cycles/gpu_rendering.html
- FreeCAD headless: https://wiki.freecad.org/Start_up_and_Configuration
- KiCad CLI: https://docs.kicad.org/master/en/cli/cli.html
- wlroots headless backend and wayvnc: https://gitlab.freedesktop.org/wlroots/wlroots,
  https://github.com/any1/wayvnc
- VirtualGL: https://virtualgl.org/
