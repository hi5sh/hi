# finder v4.0.2

A copy of `finder.js` from [@medv/finder](https://github.com/antonmedv/finder)
v4.0.2 (MIT, see LICENSE), unchanged: the CSS selector generator the hi team
helper uses in select mode. hi embeds it and serves it inside the helper's
script, without its `export` keywords (team_helper.go), so the helper needs no
build step.
