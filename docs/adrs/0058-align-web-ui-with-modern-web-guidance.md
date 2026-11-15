# ADR 0058: Align the web UI with the modern web guidance

Status: Accepted

Date: 2026-11-15

## Context

[Modern Web Guidance](https://github.com/googlechrome/modern-web-guidance) is a set of guides from the Chrome, Edge, Firefox and MDN teams. It covers HTML, CSS, accessibility, performance and security, and it steers code away from legacy patterns towards platform features. This audit read the repository at commit `4dd9683` (2026-11-10, skill version `2026_11_09-5bcc021b`): the README, the skill entry point, and the guides that apply to this UI. The guides were read directly. The guidance's `legacy-scan` command was not run.

The audit covered main at `c24bc71`: the web UI in `web/` (ADR 0029, ADR 0030) and what the Go server sends with it (`internal/ui/handler.go`, `internal/api/mux.go`). The UI is a React SPA served from the loopback admin listener. It has no sign-in, no cookies, no images other than the icons, no web fonts and no third-party code.

The guidance has two rules that shape this ADR:

- It assumes features that are **Baseline Widely available** are safe without a fallback. A feature that is only Newly available needs the fallback the guide gives, unless the project states its own browser policy. Spyglass has no stated browser policy.
- Several fixes it recommends depend on features that are Newly available (Popover, `light-dark()`, Trusted Types) or still limited (anchor positioning, `<meta name="text-scale">`).

Much of the UI is already aligned:

- One `<h1>` per view, landmarks, a labelled main navigation and a skip link (`Layout.tsx:23-50`). `lang`, `viewport` and `color-scheme` are in `index.html:2-6`.
- Charts are `role="img"` with a name and a table fallback (`ChartTable.tsx`). Decorative SVGs are hidden (`SpyglassIcon.tsx:9`, `Sparkline.tsx:30`). Generated glyphs carry empty alternative text (`styles.css:176`). An error never depends on colour alone.
- No HTML sinks: `dangerouslySetInnerHTML` is a lint error (`eslint.config.js:16-22`), and there is no third-party script.
- Hashed assets are immutable and `index.html` is `no-cache`, never `no-store` (`internal/ui/handler.go:19-20`).
- The theme script is a small classic script in `<head>`, as the dark-mode-toggle guide asks for FOUC prevention (`index.html:10-11`). The stored value is read in `try`/`catch`, and System is the default.
- `:focus-visible`, `:has()`, `scrollbar-gutter: stable`, `accent-color`, no global `*` reset. Vite 8 builds for Baseline Widely available by default, and `vite.config.ts` does not override it.

## Audit result

Gaps, with the guide that states the rule.

| # | Area | Guide | Finding | Evidence | Result |
|---|---|---|---|---|---|
| 1 | Browser policy | skill entry, "Interpreting browser support" | No stated browser policy, so every Newly available feature would need a polyfill. | `docs/architecture/frontend.md` (no section) | Not aligned |
| 2 | Security headers | `security` §1.4, §3.2, §3.4, §3.5, companions | The UI CSP is only `default-src 'self'`. There is no `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`, `X-Frame-Options`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, COOP, CORP or `Permissions-Policy`. The admin JSON responses send none of them. | `internal/ui/handler.go:24`, `:33-37`; `internal/api/mux.go:63-95` | Partly aligned |
| 3 | Trusted Types | `security` §3.3, `trusted-types` | No `require-trusted-types-for 'script'`. The code has no HTML sinks, so enforcement is cheap. | `internal/ui/handler.go:24` | Not aligned |
| 4 | Fetch Metadata | `security` §3.7 | The admin plane has no sign-in and does not reject `Sec-Fetch-Site: cross-site` requests. | `internal/api/mux.go:63-95` | Not aligned |
| 5 | Page titles | `accessibility` §4 "Titles" (WCAG 2.4.2) | Every view has the title "Spyglass". The SPA never updates it. | `index.html:7`; `router.tsx:122-130` | Not aligned |
| 6 | Focus on navigation | `html` §6, `accessibility` §5 | `main` has `tabIndex={-1}`, but a client-side navigation does not move focus to the new view. | `Layout.tsx:50`; `router.tsx` | Not aligned |
| 7 | Theme tokens | `dark-mode`, `dark-mode-toggle` | The root default is `color-scheme: dark`, not `light dark`. The light tokens are written twice. The toggle does not follow a change made in another tab, and "System" does not say which scheme the system is using. | `styles.css:12-14`, `:40-89`; `ThemeToggle.tsx:12-13`, `:4` | Partly aligned |
| 8 | Forced colours | `color` "Forced Colors Mode", `accessibility` §5 "Focus indicators" | No `forced-colors` rules. Row focus and the current page use `outline: none` with an inset `box-shadow`, which forced colours remove. | `styles.css:134`, `:242`, `:328` | Not aligned |
| 9 | Focus ring | `accessibility` §5 "Focus indicators" | The ring is 1px with `outline-offset: -1px`, inside the element. | `styles.css:116` | Partly aligned |
| 10 | Non-text contrast | `color` "Contrast" (WCAG 1.4.11) | Control borders are 1.36:1 (dark) and 1.32:1 (light) against the page. Inputs share the page background, so the border is their only boundary. | `styles.css:21`, `:49`, `:74`, `:114` | Not aligned |
| 11 | Text contrast | `color` "Contrast" (WCAG 1.4.3) | Light brass link text is 4.48:1 on white. This is the open item of ADR 0053. | `styles.css:51`, `:76`, `:104` | Partly aligned |
| 12 | Type units | `typography`, `respect-os-text-scale` | The type scale is in `px`, and `:root` sets the root `font-size`, so a reader's browser font size does not apply. | `styles.css:96-101` | Not aligned |
| 13 | Target size | `accessibility` §5 "Target sizes" (WCAG 2.5.8) | Buttons, inputs and segmented items are about 22px tall. The remove button of a filter chip is about 16px. | `styles.css:112`, `:114`, `:164`, `:203` | Not aligned |
| 14 | Overlays | `html` §4, `css-layout` §5, `viewport-aware-dropdowns` | The filter panel is an absolutely positioned `div` with a `z-index`, a document `pointerdown` listener and a hand-written Escape handler. The column menu is a `<details>` with an absolute panel and no light dismiss. | `FilterMenu.tsx:21-40`, `:62`; `DataTable.tsx:160-174`; `styles.css:196`, `:248-251` | Not aligned |
| 15 | Tooltips | `html` §5, `accessibility` §3 | `title` carries information that is not shown elsewhere: KPI notes, "client_passthrough" and "no price" notes, tool error counts, duration source, idle gaps. | `Dashboard.tsx:174`, `CostView.tsx:99`, `:105`, `ConfigView.tsx:78`, `ToolExplorer.tsx:85`, `:153`, `SessionView.tsx:245`, `:502`, `CallLog.tsx:92` | Not aligned |
| 16 | Filter semantics | `html` §1, `forms` §1 | Filter controls are not inside `<search>`. | `FilterMenu.tsx:31-65`; views' `.toolbar` | Not aligned |
| 17 | Enter in a field | `ime-safe-enter-submit` | The chip filter adds a value on `Enter` without checking `isComposing`, so an IME user adds half-composed text. | `FilterMenu.tsx:107-112` | Not aligned |
| 18 | Loading announcements | `accessibility` §8 | The loading state is a `role="status"` live region. The guide says not to announce interstitial "Loading…" states. | `states.tsx:6` | Partly aligned |
| 19 | Sort headers | `sortable-table` | Every unsorted header carries `aria-sort="none"`, and the ▲/▼ glyph is part of the button's name. | `DataTable.tsx:193`, `:198` | Partly aligned |
| 20 | Hidden text | `accessibility` §3 code example | `.sr-only` uses the deprecated `clip: rect()`. The skip link hides at `left: -999px`. | `styles.css:111`, `:121-122` | Partly aligned |
| 21 | Viewport units | `css-layout` §7, `responsive-design` | `100vh` for the shell, the sidebar and the trace. | `styles.css:123`, `:124`, `:321` | Not aligned |
| 22 | Component layout | `responsive-design`, `css-layout` §4, `respect-os-text-scale` §3 | Panel grids respond to `px` viewport media queries, not to their container or to text size. | `styles.css:137`, `:281`, `:377-385`, `:457` | Partly aligned |
| 23 | Nested scrolling | `css-layout` §6 | Nested scroll areas have no `overscroll-behavior`, so scrolling past their end scrolls the page. | `styles.css:196`, `:321`, `:445`; `DataTable.tsx:230` | Not aligned |
| 24 | Reduced motion | `motion` "Respect reduced motion" | The spinner is declared and then cancelled with `animation: none`, not opted in with `no-preference`. | `styles.css:184-189` | Partly aligned |
| 25 | Code splitting | `performance` "JavaScript Code-Splitting" | One bundle of about 530 kB (160 kB gzipped). All routes load at start. | `vite.config.ts:11-15`; `router.tsx:10-18` | Partly aligned |

Guides that do not apply to this UI:

- Images, video, `srcset`, `fetchpriority`, LCP hints and font preloading: the UI has no raster images, media or web fonts.
- Forms autofill, passkeys, payment, OTP and sign-in: the UI collects no personal data and has no sign-in.
- HSTS, cookie prefixes, `Clear-Site-Data`, CORS: the admin plane is plain HTTP on loopback, sets no cookies and sends no CORS headers (ADR 0023).
- `postMessage`, SRI, `secure-external-subresources`: no iframes, no third-party resources.
- Local Network Access: the UI is served from the loopback origin it calls, so there is no cross-address-space request.
- Speculation rules: the guide says not to use them in an SPA.
- PWA install, web app manifest, service workers: a local admin tool with no offline use.
- Built-in AI, WebMCP, WebAssembly, web components, Chrome extensions, carousels, drag and drop, swipe gestures, canvas and 3D: no such features.
- Compression of static assets: the guidance does not cover it.

## Decision

Align the UI in ten changes. Each is one implementation PR. They are ordered so that each can land alone, and the order puts policy first, then security, then accessibility, then polish.

1. **Browser policy.** The UI supports the current release of Chrome, Edge, Firefox and Safari on desktop. Baseline Widely available features are used without a fallback. Newly available features are allowed when the UI stays usable without them, by feature detection or by the browser ignoring what it does not know. Limited-availability features are progressive enhancement only. No polyfills. Record this in `frontend.md` and as one line in `AGENTS.md`, as the guidance suggests. It closes gap 1 and lets changes 4, 6 and 7 use Popover, `light-dark()` and Trusted Types without polyfills.

2. **Security headers** (gap 2). One middleware for the whole admin plane sets `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Resource-Policy: same-origin`, `Cross-Origin-Opener-Policy: same-origin`, `X-Frame-Options: DENY` and a `Permissions-Policy` that turns off camera, microphone, geolocation and similar features. The UI CSP becomes `default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'`. The UI opens no popups, so COOP `same-origin` is safe. `/metrics`, `/healthz` and `/readyz` keep working, because these headers only change what a browser does.

3. **Fetch Metadata isolation** (gap 4). The admin plane rejects a request with `Sec-Fetch-Site: cross-site` with 403, unless it is a top-level `GET` navigation to `/ui/`. A request without `Sec-Fetch-*` headers (`curl`, Prometheus, the CLI of ADR 0055) is allowed. The check runs before any handler, and admin responses add `Vary: Sec-Fetch-Site, Sec-Fetch-Mode, Sec-Fetch-Dest`. This blocks other web pages from making requests to the loopback admin plane, which has no sign-in.

4. **Trusted Types** (gap 3). Add `require-trusted-types-for 'script'` to the UI CSP. There is no policy to create, because the code writes to no HTML or script sink. The PR verifies this in a real browser: load each view with the header on and check that the console reports no violation. If React, TanStack or the Vite preload helper turns out to write to a sink, the PR stops and reports it instead of adding a pass-through policy, which the guide forbids.

5. **Page titles and focus on navigation** (gaps 5, 6). Each route sets a title with the unique part first, for example "Sessions · Spyglass" and "Session <key> · Spyglass", through the router's head support or one small hook. After a client-side navigation, but not on the first load, focus moves to the new view's `<h1>`, which gets `tabIndex={-1}`. A change of search parameters only (a filter, a sort, a range) does not move focus.

6. **Theme tokens with `light-dark()`** (gap 7). `:root` declares `color-scheme: light dark`. `data-theme="light"` and `"dark"` set `color-scheme: light` and `dark`. Each token is defined once as `light-dark(<light>, <dark>)`, so the duplicated light block goes away and the media query is no longer needed for colours. The toggle listens for the `storage` event so all tabs stay in step, and the System button names the scheme in use, for example "System (dark)". `theme-init.js` is unchanged. This amends the Consequences of ADR 0053 ("the light tokens are written twice"). The token values do not change in this step.

7. **Forced colours, focus ring and motion** (gaps 8, 9, 24). Focus uses `outline`, 2px, with a positive offset, on rows too. The inset `box-shadow` stays as decoration only. Add a `@media (forced-colors: active)` block: system colours for focus and the current page (`Highlight`, `CanvasText`), and `forced-color-adjust: none` only on chart swatches and bars, where the colour is the information. The spinner animates only under `prefers-reduced-motion: no-preference`.

8. **Contrast** (gaps 10, 11). Add a `--border-control` token with at least 3:1 against `--bg` and `--bg-elevated` in both themes, for inputs, selects and buttons. Other borders stay subtle. Darken light brass until links reach 4.5:1 on `--bg` and `--bg-elevated`. This closes the open item of ADR 0053, and the PR updates the token table there.

9. **Type in `rem` and target sizes** (gaps 12, 13). The type scale becomes `rem` (`0.6875rem`, `0.75rem`, `0.8125rem`, `0.9375rem`), and the base size moves from `:root` to `body`, so the root follows the reader's setting. Line heights become unitless. Buttons, inputs, selects, segmented items and chip remove buttons get at least 24px of target, through `min-block-size` and padding, not a fixed `height`. Virtualised rows keep a fixed height, because the virtualiser needs one, but the height is computed from the type scale. Add `<meta name="text-scale" content="scale">`, which browsers without support ignore. Breakpoints that react to text use `em`.

10. **Native overlays, semantics and layout** (gaps 14 to 23). This is the largest change, and it may land as two PRs: overlays, then the rest.
    - The filter panel and the column menu become `popover="auto"` panels opened by `popovertarget` buttons. The browser gives them the top layer, light dismiss, Escape and focus return. The `pointerdown` listener, the Escape handler and the `z-index` go away. They are placed under their button with CSS anchor positioning inside `@supports (anchor-name: --a)`. Without support, a popover shows in the browser's default centred position, which is still usable.
    - Information now only in `title` (gap 15) becomes visible text, or a note linked with `aria-describedby`. Where space is short, it becomes a small button that opens a `popover="auto"` note. A `title` that repeats text truncated in the DOM (names, ids) may stay, because the full text is already the accessible name.
    - Filter controls go inside `<search>`. The chip filter ignores `Enter` while `isComposing` is true or `keyCode` is 229. The loading state stops being a live region, and the table keeps `aria-busy`. Only the sorted header carries `aria-sort`, and the ▲/▼ glyph is `aria-hidden`. `.sr-only` uses `clip-path: inset(50%)`, and the skip link uses it too, visible on focus.
    - `100vh` becomes `100dvh`. Panel grids, the KPI grid and the call detail grid use container queries on their parent. Nested scroll areas get `overscroll-behavior: contain`.

Route-level code splitting (gap 25) is **not a change of this ADR yet**. The comment in `vite.config.ts:11-14` already sets the trigger: switch the routes to lazy loading when the bundle passes the 700 kB warning. The UI loads from loopback, so a split saves little today. The gap stays open on purpose.

## Consequences

- **Safer admin plane.** Headers, Fetch Metadata and Trusted Types close clickjacking, MIME sniffing and cross-site requests to the loopback API, at the cost of a few lines of middleware and tests.
- **Usable for more people.** Readers who use a larger font, forced colours, a keyboard, a screen reader or an IME get a working UI. Titles make browser tabs and history readable.
- **Less custom code.** The filter menu loses its outside-click and Escape code, and the stylesheet loses its second light-token block.
- **A slightly less dense look.** 24px targets and `rem` type make rows and controls a little taller at the default size. ADR 0033's density goal stays, but the minimum target wins where they conflict.
- **A browser floor.** The UI depends on current browsers. An old browser loses anchored placement of popovers and Trusted Types checks, but stays usable. This is acceptable for a self-hosted tool that its operators open in their own browser.
- **Visual tests.** Changes 6 to 10 change pixels. jsdom cannot check contrast, forced colours, popovers or anchor positioning, so each PR is checked in a real browser in both themes, as well as by the existing axe tests.
- **Open:** code splitting (above).

## Not adopted

- **CSP nonces, hashes, `'strict-dynamic'` and `'report-sample'`.** The UI has no inline script and serves its bundle from its own origin. That origin has no JSONP or user-uploaded content, and `nosniff` stops a JSON response from running as a script. Nonces would need a templated `index.html`. `'self'` stays.
- **`Reporting-Endpoints` and report-only rollout.** There is no collector for a local tool, and violations show in DevTools. Trusted Types is checked by hand in change 4 instead.
- **Cross-origin isolation** (COEP, `Document-Isolation-Policy`). Nothing in the UI needs `SharedArrayBuffer` or precise timers.
- **View transitions.** Optional polish for a dense data tool. The guides only recommend them where they help a user follow a change.
- **Temporal.** Limited availability. The UI shows millisecond times and the server sorts, so `Date` loses nothing here. Revisit when Temporal is Baseline.
- **`scheduler.yield()` and long-task splitting.** No measured long task. The Scheduler API has limited availability. Revisit if INP is measured as poor on large sessions.
- **`focusgroup`, interest invokers and `popover="hint"`.** Limited availability. The tabs are links, and change 10 uses `popover="auto"` instead.
- **Logical properties throughout.** The UI is English only. Convert a rule when it is touched and flipping it in right-to-left text makes sense, as the `css` guide asks.
- **Customizable `<select>`, `field-sizing`, `:user-invalid`.** The native selects are fine, and the filter fields have no validation styling.

## Work required

- Change 1: a "Browser support" section in `docs/architecture/frontend.md`, and one line in `AGENTS.md`.
- Change 2: a headers middleware in `internal/httpmw` applied to the admin mux, and the extended CSP in `internal/ui/handler.go`. Tests for the headers on `/ui/`, `/admin/` and `/metrics`. Update `frontend.md` "Security".
- Change 3: a Fetch Metadata middleware in `internal/httpmw` with tests for cross-site, same-origin, navigation and header-less requests. Document it in `backend.md` beside the loopback rule.
- Change 4: `require-trusted-types-for 'script'` in the CSP, and a browser check of every view, recorded in the PR.
- Change 5: route titles in `web/src/router.tsx` (or a hook used by each view) and focus on navigation in `Layout.tsx`. Tests for the title of each route and for focus after a link click and after a search-only change.
- Change 6: `light-dark()` tokens in `web/src/styles.css`, the `storage` listener and the "System (…)" label in `ThemeToggle.tsx`. Update the ThemeToggle tests and the ADR 0053 Consequences line.
- Change 7: the focus, `forced-colors` and reduced-motion rules in `styles.css`.
- Change 8: the `--border-control` token and the darker light brass in `styles.css`, the ADR 0053 token table, and `frontend.md` "Look and themes".
- Change 9: `rem` type scale, `body` base size, unitless line heights, target sizes, row heights from the scale, the `text-scale` meta in `index.html`. Check the virtualised tables and the session waterfall at 100% and 200% text size.
- Change 10: `FilterMenu.tsx` and the column menu in `DataTable.tsx` as popovers with anchored placement, the `title` replacements in the views listed in gap 15, `<search>` around the filter toolbars, the IME check, `states.tsx`, the `aria-sort` and glyph change, `.sr-only` and the skip link, `dvh`, container queries and `overscroll-behavior` in `styles.css`. Update the FilterMenu, DataTable and view tests.
- Mark this ADR Accepted when the captain approves it, and Completed in the change that lands the last item.

## Depends on

- ADR 0029 (serving and CSP), ADR 0030 (UI foundation and tables), ADR 0033 (the Linear look), ADR 0052 (the overview), ADR 0053 (palettes and theme switching).

## References

- [googlechrome/modern-web-guidance](https://github.com/googlechrome/modern-web-guidance) at `4dd968349e54ca45a05dd5c818fb035c9054b022`, `skills/modern-web-guidance/SKILL.md` and the guides named in the audit table.
- architecture/frontend.md, "Look and themes" and "Security".
- `web/src/styles.css`, `web/src/components/`, `web/src/router.tsx`, `web/index.html`, `internal/ui/handler.go`, `internal/api/mux.go`.
