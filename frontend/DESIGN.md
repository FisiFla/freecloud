---
name: FreeCloud
description: Unified identity and device management dashboard
colors:
  primary: "#4f46e5"
  primary-hover: "#4338ca"
  primary-subtle: "#eef2ff"
  primary-text: "#4338ca"
  neutral-bg: "#f8fafc"
  neutral-surface: "#ffffff"
  neutral-surface-subtle: "#f1f5f9"
  neutral-border: "#e2e8f0"
  neutral-border-subtle: "#f1f5f9"
  neutral-text-primary: "#1e293b"
  neutral-text-secondary: "#475569"
  neutral-text-muted: "#64748b"
  success: "#059669"
  success-bg: "#ecfdf5"
  error: "#dc2626"
  error-bg: "#fef2f2"
  warning: "#d97706"
  warning-bg: "#fffbeb"
  info: "#2563eb"
  info-bg: "#eff6ff"
typography:
  display:
    fontFamily: "Inter, system-ui, -apple-system, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 700
    lineHeight: 1.25
  title:
    fontFamily: "Inter, system-ui, -apple-system, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 600
    lineHeight: 1.375
  body:
    fontFamily: "Inter, system-ui, -apple-system, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Inter, system-ui, -apple-system, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 500
    lineHeight: 1.5
    letterSpacing: "0.025em"
rounded:
  sm: "6px"
  md: "8px"
  lg: "12px"
  full: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "24px"
  xl: "32px"
  2xl: "48px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    typography: "{typography.label}"
    padding: "12px 16px"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
  button-secondary:
    backgroundColor: "{colors.neutral-surface}"
    textColor: "{colors.neutral-text-secondary}"
    rounded: "{rounded.md}"
    border: "1px solid {colors.neutral-border}"
    padding: "12px 16px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.neutral-text-secondary}"
    rounded: "{rounded.md}"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.error}"
    rounded: "{rounded.md}"
  badge-success:
    backgroundColor: "{colors.success-bg}"
    textColor: "{colors.success}"
    rounded: "{rounded.full}"
  badge-warning:
    backgroundColor: "{colors.warning-bg}"
    textColor: "{colors.warning}"
    rounded: "{rounded.full}"
  card-default:
    backgroundColor: "{colors.neutral-surface}"
    rounded: "{rounded.lg}"
    textColor: "{colors.neutral-text-primary}"
    border: "1px solid {colors.neutral-border}"
  input-default:
    backgroundColor: "{colors.neutral-surface}"
    textColor: "{colors.neutral-text-primary}"
    rounded: "{rounded.md}"
    border: "1px solid {colors.neutral-border}"
---

# Design System: FreeCloud

## 1. Overview

**Creative North Star: "The Trustworthy Terminal"**

FreeCloud's visual system is designed for the IT admin who manages 10 to 10,000 devices from a browser window — seated, multi-monitor, under deadline pressure to offboard a departing employee or investigate a compliance failure. Every pixel either serves the task or is removed. The interface is restrained, technical, and trustworthy: slate neutrals carry structure, a single indigo accent signals action, and semantic color (green/red/amber/blue) is reserved exclusively for state and meaning — never decoration.

The system explicitly rejects the tropes of over-designed admin UIs: side-stripe borders, gradient icons, glassmorphism, bounce animations, and the "SaaS cream" indigo-on-gradient dashboard template. It also rejects the opposite extreme of bare-bones utility — the tool has considered empty states, skeleton loading, focus-visible rings, and error banners because the operator deserves clarity under pressure.

**Key Characteristics:**
- Restrained color: one cool indigo accent on a cool slate neutral scale. ≤10% accent coverage per screen.
- Single font family (Inter) across all roles — no display/body pairing. Hierarchy is carried by weight, size, and space alone.
- Flat-by-default elevation. Cards use a thin border (1px #e2e8f0) and a minimal shadow (`0 1px 2px 0 rgba(0,0,0,0.05)`). No large blur shadows, no tonal layering.
- Consistent page rhythm: title + subtitle → error banner → search/filter → content. Every page follows this structure.
- Dark mode with `prefers-color-scheme` auto-detection plus manual toggle. Dark surfaces use lighter-elevation layers (slate-800 → slate-900) instead of shadows for depth.

## 2. Colors

A cool-toned restrained palette anchored by indigo as the sole accent. Neutrals are true cool grays (slate scale with zero chroma). Semantic colors follow established conventions (green = success, red = error, amber = warning, blue = info).

### Primary
- **Indigo** (#4f46e5 / oklch(0.46 0.18 273)): Primary actions, active navigation items, focus indicators. Used sparingly — ≤10% of any screen's colored area.
- **Indigo Hover** (#4338ca / oklch(0.41 0.18 273)): Button hover/link hover states.
- **Indigo Subtle** (#eef2ff / oklch(0.94 0.03 273)): Selected nav background, tinted icon containers.
- **Indigo Text** (#4338ca / oklch(0.41 0.18 273)): Link text, active nav label.

### Neutral
- **Background** (#f8fafc / oklch(0.97 0.01 250)): Page-level background.
- **Surface** (#ffffff): Cards, modals, form controls.
- **Surface Subtle** (#f1f5f9 / oklch(0.96 0.01 250)): Table headers, secondary surfaces.
- **Border** (#e2e8f0 / oklch(0.93 0.01 250)): Card borders, dividers.
- **Border Subtle** (#f1f5f9 / oklch(0.96 0.01 250)): Subtle dividers.
- **Text Primary** (#1e293b / oklch(0.21 0.03 250)): Headings, body text.
- **Text Secondary** (#475569 / oklch(0.42 0.02 250)): Labels, description text.
- **Text Muted** (#64748b / oklch(0.54 0.02 250)): Placeholder text, timestamps.

### Semantic
- **Success** (#059669 / oklch(0.54 0.15 165)), **Success BG** (#ecfdf5 / oklch(0.96 0.03 165))
- **Error** (#dc2626 / oklch(0.52 0.22 30)), **Error BG** (#fef2f2 / oklch(0.96 0.03 30))
- **Warning** (#d97706 / oklch(0.65 0.15 75)), **Warning BG** (#fffbeb / oklch(0.97 0.04 75))
- **Info** (#2563eb / oklch(0.48 0.18 265)), **Info BG** (#eff6ff / oklch(0.96 0.03 265))

### Named Rules

**The Restraint Rule.** The indigo accent is used on ≤10% of any given screen. Its rarity is the point — if indigo appears more than once or twice per viewport, the interface is over-designed.

**The Semantic Color Rule.** Green, red, amber, and blue are reserved exclusively for state (success, error, warning, info). They never appear as decoration, accent, or branding.

## 3. Typography

**Body Font:** Inter (with system-ui, -apple-system, sans-serif fallback).

**Character:** Single sans-serif across all roles — no display/body pairing. Inter's technical neutrality supports the restrained, trustworthy brand without drawing attention to itself. Hierarchy is communicated entirely through weight (400 → 600 → 700), size (12px → 14px → 16px → 24px), and spacing (the page rhythm).

### Hierarchy
- **Display** (700, 1.5rem/24px, 1.25): Page titles (h1). `text-wrap: balance` applied.
- **Title** (600, 1.125rem/18px, 1.375): Card headers, section headings.
- **Body** (400, 0.875rem/14px, 1.5): Primary reading text in tables, forms, descriptions. Max line length 65–75ch on prose content.
- **Label** (500, 0.75rem/12px, 1.5, 0.025em tracking): Form labels, table header text, status badges. Uppercased in table headers; sentence-case in form labels.

## 4. Elevation

Flat-by-default. Depth is communicated through thin borders (1px #e2e8f0), not shadows or tonal layering.

### Shadow Vocabulary
- **Card shadow** (`box-shadow: 0 1px 2px 0 rgba(0,0,0,0.05)` ambient): Default card elevation. Barely perceptible; the border does the work.
- **Mobile drawer** (`box-shadow: -4px 0 24px rgba(0,0,0,0.12)`): Only applied to the mobile sidebar overlay. Single directional shadow.

### Named Rules
**The Flat-By-Default Rule.** Surfaces are flat at rest. Shadows appear only as a response to interaction (button hover, modal overlay) or semantic context (mobile drawer). A card with a 1px border does not also need a visible shadow — the border is the container signal.

## 5. Components

### Buttons
- **Shape:** Gently curved edges (8px). `rounded-lg` in Tailwind tokens.
- **Primary:** Indigo (#4f46e5) fill, white text, indigo hover. 12px 16px padding. 150ms transition.
- **Secondary:** White fill, 1px slate border, slate-600 text. Hover adds slate-50 bg.
- **Ghost:** No fill or border, slate-500 text. Hover adds slate-50 bg.
- **Danger:** Ghost treatment with error red on hover (red-50 bg, red-600 text).
- **Disabled:** 50% opacity, pointer-events: none, cursor: not-allowed on all variants.
- **Loading:** SVG spinner animation replaces children text, same dimensions.
- **Consistent height:** 40px (2.5rem) across all variants. Icon buttons are the same size with `p-2.5`.

### Badges / Chips
- **Shape:** Pill (9999px radius). `rounded-full`.
- **Variants:** success (emerald), warning (amber), info (indigo), neutral (slate).
- **Sizing:** `px-2.5 py-0.5 text-xs font-medium` — 12px, 500 weight.
- **Pattern:** Colored background + matching darker text (always the same hue, always readable contrast).

### Cards / Containers
- **Corner Style:** Rounded (12px). `rounded-xl`.
- **Background:** White; slate-900 in dark mode.
- **Border:** 1px slate-200; slate-700 in dark mode.
- **Shadow Strategy:** Flat. Ambient 1px 2px shadow only.
- **Internal Padding:** 24px (`p-6`) as default. May be set to 0 for table containers; table header + body rows provide their own spacing.

### Inputs / Fields
- **Style:** 1px slate-200 stroke, white fill, rounded (8px).
- **Sizing:** 40px min-height, 12px 16px padding.
- **Focus:** Stroke shifts to indigo-400 (1px → 1px + ring-1). Focus-visible ring (2px indigo, 2px offset) provided by global stylesheet.
- **Placeholder:** Slate-400 (#64748b, WCAG AA 4.5:1 via global override).
- **Disabled:** 50% opacity, slate-50 bg.
- **Label:** Block above the input (12px, 500 weight). Error message: 12px red-500 below.

### Navigation (Sidebar)
- **Style:** Fixed 240px column, white fill, slate-100 border-right.
- **Typography:** 14px, 500 weight. Active state: indigo-50 bg + indigo-700 text. Default: slate-500 text.
- **Mobile:** Hamburger toggle at `<lg` breakpoint. Overlay drawer with backdrop. `z-30` for sidebar, `z-40` for hamburger button.
- **Touch targets:** 44px minimum hit area (`py-2.5` + `heading` = ~44px).

### Tables
- **Structure:** Full-width table inside a Card container with `overflow-x-auto` wrapper for mobile.
- **Header row:** Slate-50 bg, 12px 500 weight uppercase tracking, `px-6 py-3` cells.
- **Body rows:** White bg, `px-6 py-4` cells, 14px slate-600 text. Hover adds slate-50 tint. `divide-y divide-slate-100` row separators.
- **Sort/filter:** Optional per-table integration. Consistent filter bar pattern above the table.

## 6. Do's and Don'ts

### Do:
- **Do** use the indigo accent exactly once per viewport section: one primary button, one active nav item, one focus ring.
- **Do** use semantic colors consistently across every screen: green for success/badges, red for errors/danger, amber for warnings, blue for informational.
- **Do** follow the page rhythm: title + subtitle → error banner → search/filter → content. Every page.
- **Do** use skeleton rows for loading states and empty-state panels (icon + title + description) for absence of content.
- **Do** use `focus-visible:ring-2` on every interactive element. Keyboard navigation is not optional.
- **Do** provide dark mode variants for every component. Dark surfaces use lighter-elevation layering (slate-800 < slate-900) instead of shadows.

### Don't:
- **Don't** use side-stripe borders (`border-left` or `border-right` greater than 1px) as colored accent. The three absolute bans in the impeccable skill apply.
- **Don't** use gradient text (`background-clip: text` + gradient). Highlight with weight or size instead.
- **Don't** use glassmorphism (`backdrop-filter: blur` as card background). Decorative blurs are banned.
- **Don't** use the hero-metric template (big number + small label + gradient accent) as a default layout. Real data stats on a dashboard are fine; decorative stats are not.
- **Don't** create identical card grids (same icon + heading + text, repeated endlessly) without varying card sizes or mixing content types.
- **Don't** place gray text (`text-slate-400`, #94a3b8) on white backgrounds for body copy. All body text must meet WCAG AA 4.5:1.
- **Don't** nest cards inside cards. Use spacing and dividers for hierarchy within.
- **Don't** animate layout properties (width, height, top, margin). Use transform and opacity only.
- **Don't** use bounce, elastic, or spring easing. Exponential ease-out curves only.
- **Don't** rely on color as the only indicator of state. Pair color with icons, labels, or text.
