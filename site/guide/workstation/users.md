---
title: Users
description: Add a user to a hi5 workstation with GPU access in one step.
---

`hi adduser` creates a user who can use the GPU straight away:

```sh
hi adduser alex
```

It runs Ubuntu's interactive `adduser`, which asks for a password and
optional details, then adds the user to the `render` and `video` groups that
GPU access needs.

Usernames follow Ubuntu's rules: lowercase letters, digits, hyphens, and
underscores, starting with a letter, at most 32 characters.

## What new users can use

System packages such as Docker, the GitHub CLI, NetBird, and ROCm work for
every user. The AI coding tools, Bun, and the compute CLIs are installed per
user, so a new user installs `hi` and picks theirs from the menu:

```sh
curl -fsSL https://hi5.sh/install.sh | sh -s -- install
```
