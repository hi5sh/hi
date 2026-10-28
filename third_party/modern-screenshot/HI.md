# modern-screenshot v4.7.0

A copy of `dist/index.mjs` from
[modern-screenshot](https://github.com/qq15725/modern-screenshot) v4.7.0
(MIT, see LICENSE), unchanged, as `modern-screenshot.js`: it draws part of a
page into a picture, which the hi team helper sends with each element the
person points at. hi embeds it and serves it at `/__hi/snap.js`; the helper
imports it the first time it needs it, so pages that never use select mode
never load it.
