# ADR 0053: Spyglass brand, palettes and theme switching

Status: Accepted

Date: 2026-11-11

## Context

The UI showed the repository name, `llm-proxy`, as plain text in the sidebar. It had no icon or favicon, and its only theme was dark. The browser tab said `llm-proxy`. The Linear look of ADR 0033 (Completed) fixed a dark palette with one indigo accent and said that error red is a status colour, not a second accent. The overview (ADR 0052) then used six muted chart hues, which were not tied to that accent.

The captain compared several brand proposals in [branding-proposals.html](../branding-proposals.html) and chose **Spyglass** with the **Lens and glint** icon. The captain also asked for a light theme, chosen by the system setting with a manual override. This ADR records those choices and amends the palette part of ADR 0033. The density, the keyboard model and the layout of ADR 0033 do not change.

## Decision

- **Name in the UI: Spyglass.** The sidebar brand, the page title and the README use it. The binary, the Go module, the commands, the config keys, the Docker image and the repository stay `llm-proxy`.
- **Icon: Lens and glint**, on a 32-unit grid: a lens ring (circle r=11, stroke 2.4), a solid pupil (circle r=4.6) and a reflection arc (path `M9.6 12.2a7.4 7.4 0 0 1 4.2-4.1`, stroke 2, opacity 0.6). The ring and the arc are brass, the pupil is teal. The sidebar draws it inline from theme tokens. `web/public/favicon.svg` follows the system scheme. `web/public/apple-touch-icon.png` is a 180 px render of the dark version on an opaque background.
- **Palettes.** Brass is the one accent. Teal is the second colour, for live and ok states and the second chart series. Error red stays a status colour.

  | Token | Dark (default) | Light |
  |---|---|---|
  | `--bg` | `#0f1011` | `#ffffff` |
  | `--bg-elevated` (surface) | `#16171a` | `#f7f7f5` |
  | `--bg-hover` | `#1c1d21` | `#f0efec` |
  | `--bg-active` | `#23252a` | `#e9e7e2` |
  | `--fg` | `#f7f8f8` | `#1b1c1e` |
  | `--muted` | `#8a8f98` | `#6b6e75` |
  | `--border` | `#2a2c31` | `#e2e0db` |
  | `--border-subtle` | `#1f2024` | `#ecebe7` |
  | `--bar` | `#4b4f58` | `#c9c7c1` |
  | `--accent` (brass) | `#e5a54b` | `#a8690f` |
  | `--accent-soft` | `rgba(229,165,75,0.16)` | `rgba(168,105,15,0.12)` |
  | `--ok` (teal, live and ok) | `#3fbfa8` | `#1c8a76` |
  | `--error` | `#eb5757` | `#d14343` |

- **Charts.** The first series is brass, the second teal, and "other" a neutral grey, in both themes. The volume chart, the timing bar and the session waterfall's call bars use brass. Sparklines and tool bars stay neutral. Every chart keeps its legend and tooltips. An error keeps a glyph and text, so it never depends on colour alone. The ok badge and the live dot use teal.
- **Theme switching.** By default the UI follows the system setting (`prefers-color-scheme`). A toggle in the sidebar footer offers System, Light and Dark. Light or Dark sets `data-theme` on `<html>`, which wins over the system setting. System removes the attribute. The browser remembers the choice in `localStorage` under `spyglass-theme`. Every storage access is in `try`/`catch`, and System is the fallback.
- **No flash.** `web/public/theme-init.js` is a small classic script in `<head>`. It applies the stored theme before first paint. It is a file and not an inline script, because the UI's CSP is `default-src 'self'` (ADR 0029).

## Consequences

- Every colour in `web/src/styles.css` is a token. The light theme only redefines tokens, so a new rule that uses tokens works in both themes.
- The light tokens were written twice in the stylesheet: once under the media query and once under `data-theme="light"`. [ADR 0058](0058-align-web-ui-with-modern-web-guidance.md) change 6 amends this: each token is now written once as `light-dark(<light>, <dark>)`, and `data-theme` sets only `color-scheme`.
- `theme-init.js` repeats the read logic of `src/lib/theme.ts`. A unit test runs the script, so the two stay in step.
- The UI and the binary have different names. The README says so near its opening.
- Open: the decided light brass (`#a8690f`) has a contrast of 4.48:1 on white and 4.17:1 on the surface. That is just under the WCAG AA minimum of 4.5:1 for small text, and link text uses this colour. Light teal (`#1c8a76`, 4.25:1 on white) is used only for fills, and the ok badge text uses `--fg`. The jsdom axe checks cannot measure contrast. A slightly darker light brass would close the gap, if a browser contrast check shows it is needed.
- ADR 0033 stays Completed and unchanged. This ADR replaces only its "monochrome with one indigo accent, dark only" palette.

## Work required

- Tokens for both themes, the system media query and the `data-theme` overrides in `web/src/styles.css`.
- The theme toggle in the sidebar footer, `src/lib/theme.ts`, and `public/theme-init.js` loaded from `index.html`.
- The Lens and glint icon in the sidebar, the page title, `favicon.svg` and `apple-touch-icon.png`.
- Tests: the toggle, its persistence and the storage fallback, the pre-paint script, and the axe check in both themes.
- Docs: the README opening, [frontend.md](../architecture/frontend.md) "Look and themes", and [branding-proposals.html](../branding-proposals.html) under `docs/`.

## Depends on

- ADR 0029, ADR 0033, ADR 0052.

## References

- [branding-proposals.html](../branding-proposals.html): the proposals and the chosen Spyglass mark.
- architecture/frontend.md, "Look and themes".
- ADR 0033 (the Linear look this ADR amends), ADR 0029 (CSP and serving).
