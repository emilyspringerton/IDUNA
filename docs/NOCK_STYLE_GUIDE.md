# NOCK — Style Guide

Founder real-time (2026-09-14): "can we add daisy UI it plugs in to tailwind to make NOCK look
nicer? have the style guide for specifically the NOCK TOLLS updated to the default daisy UI
colors bright and colorful - the buttons and fields look kind of jank right now u did a good job
but we can make it look like damn what app is that... its the Nock tools my sir" → "have a light
mode and a dark mode both colorful."

This doc is the real, current answer: what theme NOCK runs, why those two themes specifically,
and the one rule every future NOCK component/page should follow to stay themed automatically.

## The two real themes

NOCK runs on **daisyUI 5** (`@plugin "daisyui"` in `frontend/nock/src/index.css`), plugged
straight into the existing Tailwind v4 CSS-first setup — no separate `tailwind.config.js`, no
JS-side plugin array, matching Tailwind v4's own convention this app already used.

Two real, **unmodified, stock daisyUI themes** — not hand-tuned custom colors, per the founder's
own explicit "default daisy ui colors" instruction:

| | Theme | Mode | Why |
|---|---|---|---|
| Default | `cupcake` | light | Warm, colorful, playful out of the box — the real daisyUI theme most often cited as "just looks good" for a creative tool, which is exactly what NOCK is (a texture/level editor, not a back-office form). |
| Dark | `dracula` | dark | Vivid purple/pink/cyan/green accents on a real dark base — colorful without being straining to stare at, a well-known, well-loved daisyUI theme. |

Both are declared in `index.css`:

```css
@plugin "daisyui" {
  themes: cupcake --default, dracula --prefersdark;
}
```

`--prefersdark` means `dracula` applies automatically when the OS is in dark mode, with zero JS
— a real user who's never touched NOCK before gets the right theme for their system immediately.

## The manual toggle

`App.tsx`'s own `useTheme()` hook adds a real, explicit override on top of the OS default,
persisted in `localStorage` (`nock-theme`) and applied via `document.documentElement.dataset.theme`
— the same real "OS default, explicit override wins" contract most theme toggles use. The toggle
button lives in the header (`.theme-toggle`).

## The one rule for staying themed: never hardcode a color

`index.css`'s own `@theme` block aliases NOCK's existing semantic token names (`bg`, `panel`,
`line`, `ink`, `muted`, `gold`, `gold-content`, `danger`) directly to daisyUI's real theme
variables:

```css
@theme {
  --color-bg: var(--color-base-100);
  --color-panel: var(--color-base-200);
  --color-line: var(--color-base-300);
  --color-ink: var(--color-base-content);
  --color-muted: color-mix(in oklch, var(--color-base-content) 60%, transparent);
  --color-gold: var(--color-primary);
  --color-gold-content: var(--color-primary-content);
  --color-danger: var(--color-error);
}
```

Because these are real CSS custom properties, `bg-panel`/`text-gold`/`border-line`/etc. re-theme
themselves automatically the instant `data-theme` changes — no per-component JS, no duplicate
light/dark class variants anywhere in this codebase. **The rule this buys: never write a literal
hex color in a NOCK component or its CSS.** Use the semantic tokens above (or a real daisyUI
utility like `text-error`/`bg-primary` directly) so it "just works" in both themes. A hardcoded
color is the one thing that silently breaks this — found and fixed one real instance during this
same pass (`.project-list li.active button` was `text-[#1a1509]`, baked-in for `cupcake`'s own
primary-content only; now `text-gold-content`, correct in both themes).

Known, real, accepted exception: `LevelEditor.tsx`'s 2D `<canvas>` platform-editor fill colors
(`ctx.fillStyle = '#c9a24a'` etc.) — canvas 2D drawing can't consume a CSS custom property
directly without extra JS to read the computed style first. Real, named, not fixed in this pass.

## Base element styling

Bare `<button>`/`<input>`/`<select>`/`<textarea>` elements across every NOCK page already pick up
daisyUI's look for free via `index.css`'s own element-selector rules (`button { @apply btn
btn-primary btn-sm; }`, etc.) — no need to add daisyUI classes to every individual JSX call site.
Component-specific `@apply` rules in `App.css` (e.g. `.tabs button`, `.spray-card`) layer daisyUI
utility/component classes (`btn-outline`, `btn-error`, card-shaped borders) on top of that base,
following the same "named component class, not inlined utility strings" convention this file's
own header comment already establishes.

## Adding a new NOCK page

1. Use plain `<button>`/`<input>`/`<select>` — they're themed automatically.
2. Reach for the semantic tokens (`text-muted`, `bg-panel`, `border-line`, `text-gold`) or a real
   daisyUI utility (`btn-error`, `badge-success`) before ever writing a literal color.
3. If you need a genuinely new component look, add a named class to `App.css` using `@apply` with
   daisyUI's own utility/component classes (see daisyUI's docs for the full class list — `card`,
   `badge`, `alert`, `modal`, `tabs`, etc. are all available since the plugin is loaded globally).
