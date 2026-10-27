# Vendored browser libraries

Copied byte-identical from the npm registry tarballs and embedded in the
`kubedrill` binary, so the browser workspace needs no network. MIT license
in `xterm/LICENSE`.

| File | Package | Tarball sha1 (npm `dist.shasum`) | File sha256 |
|------|---------|----------------------------------|-------------|
| `xterm/xterm.js` | `@xterm/xterm@5.5.0`, `lib/xterm.js` | `275fb8f6e14afa6e8a0c05d4ebc94523ff775396` | `1f991ac3b4b283ebf96e60ae23a00a52765dd3a2e46fa6fdda9f1aab032f7495` |
| `xterm/xterm.css` | `@xterm/xterm@5.5.0`, `css/xterm.css` | same tarball | `ba8e6985669488981ccf40c0cefe3aba80722cb6c92de7ad628b0bd717faf2b6` |
| `xterm/addon-fit.js` | `@xterm/addon-fit@0.10.0`, `lib/addon-fit.js` | `bebf87fadd74e3af30fdcdeef47030e2592c6f55` | `bdaefa370b1bfc42ee88d46fe6072400902a4d4b2d45cd93438dda9b23c97089` |

To bump: download `https://registry.npmjs.org/<package>/-/<name>-<version>.tgz`,
compare its sha1 with `dist.shasum` from `https://registry.npmjs.org/<package>/<version>`,
copy the files and update this table.
