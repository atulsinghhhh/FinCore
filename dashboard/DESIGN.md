# FinCore Dashboard — Design System

> white gallery with a neon pulse — a clean Helvetica poster where one hot pink line cuts through the black-and-white calm.

**Theme:** light, with a dark mode · **Source:** style reference "sunday" (visual language only; FinCore keeps its own name and mark).
Tokens are implemented in [`app/globals.css`](app/globals.css); primitives in [`components/ui.tsx`](components/ui.tsx).

## How FinCore applies it (admin console adaptations)

The reference is a marketing site; FinCore is a data-dense operations console. These adaptations keep the language intact:

| Reference rule | FinCore application |
|---|---|
| Neon pink only as a signal | Logo mark, active nav underline, one emphasized word per page headline, badge borders, and **attention states** (failed / dead / mismatch / exceptions) as a pink-bordered badge with a pink dot. Never fills, never body text. |
| No second accent color | Status uses shape and fill, not hue: **settled** = solid black pill, **in progress** = black outline, **neutral/terminal** = mist outline, **needs attention** = pink outline. |
| 200px stat numerals | One hero figure per page (`StatHero`), fluid `clamp(64px, 13vw, 200px)` so long ₹ amounts fit. Secondary stats at 48px. |
| +0.08em on ≤14px | Applied to uppercase labels, table headers and badges. Mixed-case data cells use 14px with normal tracking for legibility of IDs and amounts. |
| Black bands for tonal breaks | The overview's system-health section is a full-bleed black band with black cards. |
| Weight 400 only | No bold anywhere; hierarchy is size + tracking. Numbers use tabular figures. |

---

## Dark mode

Dark mode inverts the gallery: black canvas, white type, the same single neon pulse. Every color is a CSS variable (`--c-*` in `app/globals.css`), so components never branch on theme.

| Token | Light | Dark |
|---|---|---|
| ink (text, primary fills) | `#000000` | `#ffffff` |
| paper (canvas, cards) | `#ffffff` | `#000000` |
| warm-gray | `#736f7c` | `#a29fab` |
| mist (hairlines) | `#dedede` | `#2b2b2b` |
| fog | `#bdbdbd` | `#5f5f5f` |
| slate | `#7f7f7f` | `#8f8f8f` |
| band (tonal break) | `#000000` | `#111111` |
| band card | `#111111` | `#1c1c1c` |
| pulse | `#ff17e9` | `#ff17e9` |

- Primary buttons invert automatically (white pill, black text in dark).
- Dark **bands** (`.band`) keep their white-on-black design in both themes; in dark mode the band lifts to `#111` so it still reads as a break from the black canvas.
- The theme follows the OS by default; the header toggle saves an explicit choice in `localStorage`, applied by an inline script before first paint (no flash).

## What makes this a complete design system

A palette and a font are a style; a design system also answers *how* things behave, *who* can use them, and *how it changes*. FinCore's covers:

| Layer | Where it lives | Status |
|---|---|---|
| Principles | below | ✅ |
| Tokens (color, type, space, radius, elevation, motion) | `app/globals.css` (`@theme`), this file | ✅ |
| Light / dark themes | `--c-*` variables, `.band` | ✅ |
| Accessibility rules (contrast, focus, motion) | below | ✅ |
| Component inventory with states | below, `components/ui.tsx` | ✅ |
| Data formatting (money, dates, IDs) | `lib/format.ts`, below | ✅ |
| Content voice | below | ✅ |
| Layout & breakpoints | below | ✅ |
| Governance (how to change it) | below | ✅ |
| Charts | below, `components/charts.tsx` | ✅ |
| Visual regression tests, Figma library, Storybook | — | next steps |

## Principles

1. **Type does the work.** One family, one weight; hierarchy from size and tracking.
2. **One pulse.** Neon pink marks what needs attention, nothing else.
3. **Show the money exactly.** Amounts are tabular, in ₹ with paise, never rounded for looks.
4. **State without hue.** Status is fill and outline, so it survives color blindness and dark mode.
5. **Flat and honest.** No decorative shadows or gradients on controls; elevation only for the sticky nav.

## Accessibility

**Contrast** (WCAG 2.2; AA needs 4.5:1 for normal text, 3:1 for large text ≥ 24px or ≥ 18.66px bold):

| Pair | Ratio | Allowed for |
|---|---|---|
| ink on paper | 21:1 | everything |
| warm-gray on white | 4.89:1 | body and secondary text ✅ |
| slate on white | 4.00:1 | large text, inactive nav labels only |
| fog on white | 1.88:1 | **placeholders, disabled, decoration only — never text** |
| pulse on white | 3.21:1 | **large text (headline words) and graphics only — never small text** |
| warm-gray on black (dark) | 8.08:1 | everything |
| pulse on black (dark / bands) | 6.55:1 | everything |
| fog on a band (#bdbdbd on #000) | 11.18:1 | everything |

Rules that follow: small pink labels use an ink label plus a pink dot; numbered labels, badges and alerts never rely on pink text alone.

- **Focus:** every interactive element shows a 1px ink outline (`:focus-visible`), offset 2px. Never remove it.
- **Motion:** all animation respects `prefers-reduced-motion` (animations are switched off, content is shown immediately).
- **Semantics:** tabs use `role="tablist"`/`aria-selected`; pagination is a labelled `<nav>`; decorative dots and sparkles are `aria-hidden`; animated headlines keep their full text in `aria-label`.
- **Targets:** controls are at least 36px tall (pills, toggle), comfortably tappable on touch.

## Component inventory and states

| Component | File | States |
|---|---|---|
| Button (filled / ghost / small) | `ui.tsx` `buttonClass` … | default · hover (opacity / invert) · focus · disabled (40%) · pending text ("Signing in…") |
| Input / select | `ui.tsx` `inputClass` | default (mist) · focus (ink border) · placeholder (fog) · error (pink dot + message below, never a red border) |
| Badge | `ui.tsx` `Badge` | settled · moving · neutral · attention |
| Card / DarkCard / DarkBand | `ui.tsx` | static; bands keep white-on-black in both themes |
| Table | `ui.tsx` `Table`, `Td` | default · numeric (right, tabular) · mono IDs · muted · **empty** ("Nothing here yet.") |
| Tabs (link pills) | `ui.tsx` `Tabs` | active (ink fill) · inactive · hover |
| Pager | `pager.tsx` | first page (Newest disabled) · middle · last page (Older disabled) |
| Mode switch | `mode.tsx` | test (pink dot) · live · live unavailable (disabled, explained in tooltip) |
| Theme toggle | `theme-toggle.tsx` | light · dark (icon driven by CSS) |
| Loading | `Skeleton` | pulsing mist bars inside `<Suspense>` |
| Error | `app/error.tsx` | pink "Error" tag, message, retry button |

Every data view must define **loading, empty and error** states before it ships.

## Data formatting

- **Money:** `money(minor, currency)` → `₹1,23,456.78` (Indian grouping, always two decimals, tabular figures, right-aligned in tables).
- **Dates:** `dateTime(iso)` → `23 Nov 2026, 4:04 pm`, Asia/Kolkata.
- **IDs:** first 8 characters in mono, full ID on detail pages.
- **Counts:** `count(n)` with Indian grouping.

## Content voice

Short, declarative, exact. Headlines state a guarantee ("Debits equal credits. Always."). Errors say what happened and what to do, never blame ("The merchant's available balance is too low for this operation."). Use the product's real nouns — payment, refund, payout, ledger, settlement — never "transaction" loosely.

## Layout & breakpoints

Max width 1200px, 16px gutters (24px from `md`). Breakpoints follow Tailwind: `sm` 640, `md` 768, `lg` 1024, `xl` 1280. Grids collapse to one column below `lg`; wide tables scroll horizontally inside their card instead of squeezing columns.

## Motion tokens

| Token | Value | Use |
|---|---|---|
| `--ease-out-expo` | `cubic-bezier(0.16, 1, 0.3, 1)` | every entrance |
| word stagger | 140 ms | headline words |
| letter stagger | 75 ms | neon-flash phrase |
| entrance | 1.5 s rise 15% + fade | titles, blocks |
| tab cycle | 6.5 s | auto-advancing tabs |

## Charts

Built by hand in SVG (`components/charts.tsx`), following a data-visualisation method:
- **Form first:**
  - single numbers → KPI tiles (value, delta against the previous period, sparkline)
  - trends → a single-series area chart
  - part-to-whole → **ranked horizontal bars**, never a donut or pie
  - two related series → stacked columns
- **Marks:**
  - 2px lines and a 10% area wash
  - bars at most 24px, with a 4px rounded data end and square at the baseline
  - 2px gaps between stacked segments
  - hairline, solid, recessive gridlines and clean tick values (`₹25K`, `₹1L`)
- **Color is emphasis, not categories:** the series that matters (failures, the current value) uses the pulse; context uses `--c-chart-context` (#8a8794 light / #8f8f8f dark). The pair passes colour-blind separation (ΔE 11.8 light, 12.4 dark; target ≥ 8) and 3:1 contrast in both themes.
- **Text** never takes a series colour; identity comes from swatches and the legend, which is always shown for two or more series.
- **Every chart** has a hover layer (crosshair or per-mark tooltip) and a "Data table" fallback.
- **Deltas** show direction with an arrow; a bad direction adds a pink dot rather than a red/green pair.

## Governance

- Tokens change in `globals.css` **and** this file in the same PR.
- New colors are not added; new semantic needs map onto existing tokens.
- A component is added to `ui.tsx` only once it is used in two places; until then it lives with its page.
- CI runs typecheck, lint and a production build for both apps; contrast rules above are checked in review.

## Tokens — Colors

| Name | Value | Token | Role |
|------|-------|-------|------|
| Carbon Black | `#000000` | `--color-ink` | Primary text, hairlines, filled primary buttons, dark section backgrounds, and card surfaces on dark bands |
| Pure White | `#ffffff` | `white` | Page canvas, card surfaces, button text on dark fills, form input fills |
| Warm Gray | `#736f7c` | `--color-warm-gray` | Body text on light surfaces, subtle borders, muted helper text |
| Mist | `#dedede` | `--color-mist` | Hairline dividers, input borders, disabled states |
| Fog | `#bdbdbd` | `--color-fog` | Placeholder text, inactive borders, secondary dividers |
| Slate | `#7f7f7f` | `--color-slate` | Inactive nav text, subtle metadata |
| Iris Shadow | `#8b8893` | `--color-iris` | Rare shadow tint and muted border accent |
| Neon Pulse | `#ff17e9` | `--color-pulse` | Brand accent — logo mark, active nav underline, highlighted words, badge borders |

## Tokens — Typography

**Helvetica Neue** (substitutes: Helvetica, Inter, Arial), weight **400** only, 12px → 200px. Positive tracking (+0.08em) on the smallest uppercase labels; aggressive negative tracking (-0.05em) on display sizes.

| Role | Size | Line height | Letter spacing | Utility |
|------|------|-------------|----------------|---------|
| caption | 12px | 1.78 | +0.08em | `text-caption` |
| label | 14px | 1.78 | +0.08em | `text-label` |
| data *(FinCore)* | 14px | 1.5 | 0 | `text-data` |
| body | 16px | 1.33 | -0.01em | `text-body` |
| subheading | 18px | 1.33 | -0.01em | `text-subheading` |
| heading-sm | 24px | 1.2 | -0.01em | `text-heading-sm` |
| heading | 32px | 1.13 | -0.03em | `text-heading` |
| heading-lg | 48px | 1.13 | -0.03em | `text-heading-lg` |
| display | 64px | 0.95 | -0.05em | `text-display` |
| display-xl | 200px | 0.8 | -0.05em | `text-display-xl` |

Tracking zones: ≤14px → +0.08em · 16–24px → -0.01em · 32–48px → -0.03em · 64px+ → -0.05em.

## Tokens — Spacing & Shapes

Base unit **8px** · comfortable density · page max-width **1200px** · section gap **80px** · card padding **24px** · element gap **8px**.

| Element | Radius | Token |
|---------|--------|-------|
| nav, cards, inputs | 16px | `rounded-card` |
| link pills / tabs | 24px | `rounded-link` |
| large cards | 48px | `rounded-large` |
| buttons | 64px | `rounded-pill` |
| badges / tags | 100px | `rounded-tag` |

**Elevation:** only the sticky navigation header — `rgba(0,0,0,0.15) 0 12px 60px 0` (`shadow-nav`). Cards, buttons and inputs are flat.

## Components

- **Primary filled button** — black fill, white text, 64px pill, 16px × 24px padding, 16px text. No shadow, no border.
- **Ghost button** — transparent, 1px black border, same pill and padding.
- **Navigation header** — sticky white bar, 1px mist bottom border, nav shadow. Logo left (pink mark + black wordmark), links in black at 14–16px, active link marked by a 2px pink underline.
- **Form input** — white, 1px mist border, 16px radius, 16px padding, 16px black text, fog placeholder; focus darkens the border to black. No color for states.
- **Card** — white, 1px mist border, 16px radius, 24px padding, no shadow.
- **Dark feature card** — black on a black band, 16px radius, no border, white text.
- **Stat display** — oversized numeral, weight 400, line-height 0.8, -0.05em, label below at 14px in warm gray.
- **Brand badge** — transparent, 1px pink border, 100px pill, 12px uppercase +0.08em black text.
- **Footer** — 1px mist top border, 12px uppercase warm-gray headings, 14px black links.

## Do

- Use `#ff17e9` only as a signal — logo mark, active nav underline, highlighted words in headlines, badge borders.
- Primary actions: black fill, white text, 64px pill.
- One family, weight 400; hierarchy from scale and tracking only.
- -0.05em at 64px+; +0.08em on 12–14px uppercase labels.
- Borders default to 1px `#dedede`; black borders only for focus/active or structural dividers.
- Cards and inputs at 16px; buttons 64px; tags 100px.

## Don't

- Introduce a second accent color.
- Add shadows to cards, buttons or inputs.
- Use multiple font weights.
- Use pink for fills or large text blocks — it is a pulse, not a wash.
- Round buttons at 8px or 12px.
- Use color for form field states — darken the border instead.
- Use gray page backgrounds — use black bands for tonal breaks.
