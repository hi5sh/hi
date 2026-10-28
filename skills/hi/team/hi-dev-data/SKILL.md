---
name: hi-dev-data
description: See the hi team's app data from its dev box, and give the preview a fresh fork of production's data. Use when the user asks about the app's data, its size, the preview showing old or wrong data, or wants to test against current production data. Covers hi dev data.
---

# The app's data

```bash
hi dev data          # production's and the preview's data: size, files, when it changed
hi dev data fork     # the preview gets a new fork of production's data now
```

The app keeps its data in a folder of its own (`$HI_DATA_DIR` in the app).
Production's is the people's. The preview runs on a **fork** of it: it
reads production's data as it was when forked, and what it writes stays
its own, so trying things on the preview never changes production.

- `hi dev preview` forks again by itself when new work goes on the
  preview. Use `hi dev data fork` when the preview's data is stale or a
  test messed it up.
- Production pauses for a few seconds while it is forked.
- The box can't change production's data: copying the preview's data to
  production is an admin's, on the machine.
- To see the data, use the app on the preview, or its API: the data
  isn't in the box.
