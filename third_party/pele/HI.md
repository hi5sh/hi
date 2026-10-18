# pele v1.6.0

A copy of `dist/pele.js` from [pele](https://github.com/obsidianmd/pele)
v1.6.0 (MIT, see LICENSE), unchanged: it draws Mermaid charts as SVG, every
diagram type Mermaid has built in, themed with CSS variables. hi embeds it
and serves it at `/docs/pele.js` on a team's addresses, where the docs pages
draw their `mermaid` blocks with it (team_docs.go); the guide loads this
copy for its own charts (_layouts/guide.html). It makes no requests of its
own and runs no code from the charts.
