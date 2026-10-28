---
name: hi-dev-settings
description: See the names of the hi team app's settings (environment variables such as API keys) from its dev box, and set the preview's own values. Use when the app needs a key or setting, the preview lacks one, or the user asks what settings the app gets. Covers hi dev settings.
---

# The app's settings

```bash
hi dev settings                      # the names of the settings the app gets; never values
hi dev settings QUOTES_URL=https://sandbox.example.com
printf %s "$VALUE" | hi dev settings QUOTES_KEY     # a secret from stdin
hi dev settings --unset QUOTES_URL   # the preview no longer gets its own value
```

The app gets its settings as environment variables. The box sets **the
preview's own values**: production keeps what it has. A setting marked
"production only" isn't in the preview at all, often a key that sends,
posts, orders, or pays per call; the app must do without it there, or the
preview gets a test key of its own.

A setting marked "a file" is a file, such as a database's certificate
with its private key: its variable holds the file's path in the box
(`/hi-files/<NAME>`, read-only). Hand the path to the library
(`tlsCertificateKeyFile` for MongoDB); never copy or print the file.

This dev box gets only its own settings, which an owner gives it
(`hi team env <team> --dev`), never the preview's or production's: a
test database's address and certificate, say. They are in your
environment already; `env` shows their names. When the work needs one it
lacks, tell the user which, and that an owner sets it for the dev box.

- The next preview has a change: run `hi dev preview` again to use it now.
- No value is ever shown, here or anywhere: don't ask for one back.
- Production's settings are an admin's, on the machine
  (`hi team env <team> KEY=VALUE`, or `--file KEY=<path>` for a file). When production needs a new key, tell
  the user which one, and that an admin sets it.
- Don't put secrets in the code, the tasks, or Slack.
