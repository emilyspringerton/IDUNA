# IDUNA — Style Guide (original institutional cream/gold)

The original ceremonial IDUNA look: warm parchment, gold hairlines, serif type. Source of truth is
`styles.css` + `index.html` at the repo root; `/portal` and `/admin/login` were restyled to it
(2026-08-25). The NOCK tools use a separate system: see [NOCK_STYLE_GUIDE.md](NOCK_STYLE_GUIDE.md).

## Color tokens (`styles.css :root`)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#f3ede2` | page background (cream/parchment), with a radial gradient |
| `--panel-bg` | `#f8f3ea` | panels |
| `--gold` | `#b89b62` | borders, brand mark, CTA outline |
| `--text-main` | `#3f3a34` | body text |
| `--text-soft` | `#6f6860` | secondary text |
| `--text-whisper` | `#847c73` | microtext |
| `--consent-rose` | `#b76e79` | consent / confirm button (hover `#c28793`, highlight `#d8a7a7`) |
| `--consent-disabled` | `#9a9288` | disabled controls |

## Type

- Headlines: **Cormorant Garamond** (400-600), `clamp(2rem, 4vw, 2.8rem)`.
- Body / ceremony text: **Spectral** (400-500). UI fallback: Inter, Segoe UI.
- Brand mark: uppercase, `letter-spacing: 0.28em`, `0.72rem`, gold.
- Microtext: `0.78rem`, `--text-whisper`.
- Fonts: Google Fonts link in `index.html`; OFL files bundled under `assets/fonts/` where offline.

## Components

- **Panel:** `1px` gold border (gold mixed 72% with white), `8px` radius, `--panel-bg`.
- **Honor-code box:** lighter gold border (42%), `6px` radius.
- **Buttons:** `5px` radius, `1px` border, `0.02em` tracking, `160ms` color transitions.
  CTA = gold outline; consent = rose outline with `0.04em` tracking; disabled = muted grey.
- **Layout:** full-height viewport, content centered in a single panel; calm, no neon, no heavy shadows.

## Rules

- Light only. No dark mode in this system (dark/neon belongs to DEADWEIGHT; light+dark colorful belongs to NOCK).
- Gold is structure (borders, marks), rose is consent action. Don't swap them.
- Real Prompt-o-verse art only for illustrations on login surfaces.
