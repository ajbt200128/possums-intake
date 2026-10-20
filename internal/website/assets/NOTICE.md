# Bundled font assets — Iosevka Term

This directory holds the pinned static font binaries served by the local website
packet, plus the license text and a machine-readable manifest. It contains no
generated CSS, no Go code and no build step. The parent packet decides how these
fixed paths are served; this acquisition packet only places and records them.

## What is bundled

| File | Content |
| --- | --- |
| `iosevka-term-regular.woff2` | Iosevka Term upright regular (weight 400) |
| `iosevka-term-bold.woff2` | Iosevka Term upright bold (weight 700) |
| `OFL.txt` | Full SIL Open Font License 1.1 text plus the upstream copyright line |
| `manifest.json` | Paths, sizes, SHA-256 values and license metadata |

Only the two upright weights above are bundled. Italic and oblique faces, the
`Extended` width faces and every other weight in the upstream package are
deliberately **not** included, so no synthesized or unintended bold can be
selected by accident. The site plan asks for real 400 and 700 faces with a
generic monospace fallback; if the font is blocked (for example by strict Tor
Browser settings) the pages must still render legibly in the fallback.

## Exact sources

- Release archive (all files extracted from here):
  `https://github.com/be5invis/Iosevka/releases/download/v34.9.0/PkgWebFont-IosevkaTerm-34.9.0.zip`
  - SHA-256: `249ad573bbddb26311956b1c6138035dd1d457bae13017b173b182fe59324ba5`
  - Size: 270,966,110 bytes
  - Selected zip members: `WOFF2/IosevkaTerm-Regular.woff2`, `WOFF2/IosevkaTerm-Bold.woff2`
- License text (the archive ships **no** license file, so it was fetched
  separately from the same tag):
  `https://raw.githubusercontent.com/be5invis/Iosevka/v34.9.0/LICENSE.md`
  - SHA-256: `4ba53c7c1cb39279aae5f8d7d22054c485c71169920e5a36ed098b115e2e3c5d`
  - Saved here as `OFL.txt`, unmodified.

Both requests used TLS with normal certificate verification over the standard
HTTPS port, with no credentials, tokens or global installs. Redirects were
accepted only to allowlisted GitHub hosts; the request targets themselves are
fixed constants, not user input.

## Selected-file hashes

| File | Bytes | SHA-256 |
| --- | --- | --- |
| `iosevka-term-regular.woff2` | 1,703,712 | `2921da08b32396422927bb76e3618938e13ede0b3dfb1785d5ca9d88df863752` |
| `iosevka-term-bold.woff2` | 1,724,824 | `3f9db216e17c47e03fd0c392b17e3a2f7cbd568c1bc4a8dfb165a106df273753` |
| `OFL.txt` | 4,493 | `4ba53c7c1cb39279aae5f8d7d22054c485c71169920e5a36ed098b115e2e3c5d` |

Every hash above was recomputed from the files as they sit in this directory,
not copied from the download step.

## Licensing

Iosevka is released under the SIL Open Font License 1.1. The upstream copyright
notice is:

    Copyright (c) 2015-2026, Renzhi Li (aka. Belleve Invis, belleve@typeof.net)

The complete OFL 1.1 text is preserved verbatim in `OFL.txt` and must ship
alongside the font binaries. The OFL requires the copyright notice and license
to accompany redistributed copies. Its reserved-name condition applies to names
explicitly declared reserved; the supplied copyright block does not declare one.
These files are redistributed unmodified; the complete license governs reuse.

## Verification performed and its limits

Checks actually run in this packet:

- The archive's zip directory was listed and both required members were present
  at their exact expected paths.
- Each extracted file begins with the WOFF2 signature `wOF2` (0x774F4632),
  declares TrueType flavor `0x00010000`, is at least the 48-byte WOFF2 header
  long, and its declared length field equals the real byte length.
- The SHA-256 values in `manifest.json` were re-read from disk independently of
  the extraction step and matched.
- The release's own `IosevkaTerm.css` maps these exact filenames to
  `font-family: 'Iosevka Term Web'`, `font-style: normal`, and weights 400 and
  700 respectively.

Known limits:

- The font binaries' internal `name`/`OS/2` tables were **not** parsed.
  `fontTools` is not present in this environment and installing it was out of
  scope, so the weight/family labels above come from the release's accompanying
  CSS rather than from the font's own metadata. Do not restate them as parsed
  font-table evidence.
- Glyph coverage, hinting, variable-instance behavior and rendering appearance
  were not inspected. Visual review against real regular/bold rendering remains
  a separate step in the website plan.
- This packet recorded provenance for the pinned release and files only. It
  makes no claim about any other Iosevka release or build.
