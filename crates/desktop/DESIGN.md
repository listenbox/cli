---
name: Listenbox Desktop Native
description: A calm GPUI Kit podcast workspace that projects Listenbox's achromatic system into a native control room.
colors:
  background-light: "oklch(0.9851 0 0)"
  background-dark: "oklch(0.2134 0 0)"
  sheet-light: "oklch(1 0 0)"
  sheet-dark: "oklch(0.252 0 0)"
  rail-light: "oklch(0.9612 0 0)"
  rail-dark: "oklch(0.2478 0 0)"
  ink-light: "oklch(0.2435 0 0)"
  ink-dark: "oklch(0.9702 0 0)"
  muted-light: "oklch(0.4532 0 0)"
  muted-dark: "oklch(0.8015 0 0)"
  border-light: "oklch(0.8761 0 0)"
  border-dark: "oklch(0.4349 0 0)"
  divider-light: "oklch(0.9219 0 0)"
  divider-dark: "oklch(0.3446 0 0)"
  selected-light: "oklch(0.9219 0 0)"
  selected-dark: "oklch(0.3368 0 0)"
  action-light: "oklch(0.3092 0 0)"
  action-dark: "oklch(0.4532 0 0)"
  action-ink: "oklch(1 0 0)"
  danger-light: "#b44d42"
  danger-dark: "#ef9a8d"
typography:
  page-title:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: "30px"
    fontWeight: 700
  title:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: "18px"
    fontWeight: 600
  body:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
  label:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 600
  support:
    fontFamily: "ui-sans-serif, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 400
rounded:
  control: "10px"
spacing:
  rail: "240px"
  content: "24px"
  control-gap: "12px"
  row-gap: "8px"
  row-padding-y: "12px"
components:
  button-primary-light:
    backgroundColor: "{colors.action-light}"
    textColor: "{colors.action-ink}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  button-primary-dark:
    backgroundColor: "{colors.action-dark}"
    textColor: "{colors.action-ink}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  button-ghost-light:
    textColor: "{colors.ink-light}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  button-ghost-dark:
    textColor: "{colors.ink-dark}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  input-playlist-light:
    backgroundColor: "{colors.sheet-light}"
    textColor: "{colors.ink-light}"
    rounded: "{rounded.control}"
  input-playlist-dark:
    backgroundColor: "{colors.sheet-dark}"
    textColor: "{colors.ink-dark}"
    rounded: "{rounded.control}"
  navigation-selected-light:
    backgroundColor: "{colors.selected-light}"
    textColor: "{colors.ink-light}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  navigation-selected-dark:
    backgroundColor: "{colors.selected-dark}"
    textColor: "{colors.ink-dark}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
  transfer-row-light:
    textColor: "{colors.ink-light}"
    padding: "12px 0"
  transfer-row-dark:
    textColor: "{colors.ink-dark}"
    padding: "12px 0"
---

# Design System: Listenbox Desktop Native

## Overview

**Creative North Star: "The Calm Native Control Room"**

This desktop surface is a native projection of Listenbox's established
recording-studio identity. It gives the Operate job a quiet rail, a clear
selected-show workspace, and a transfer queue that reports real work. The
implementation uses GPUI Kit controls and the operating system's type and
appearance; it does not create a second visual world.

The ground is achromatic white, silver, and charcoal in both appearance modes.
Color is reserved for semantic status, while hierarchy comes from tone,
spacing, borders, and native control states. The desktop surface stays compact
and task-first even though the web identity also has spacious marketing
compositions. The web `DESIGN.md` remains the authority for shared roles; this
file records the native projection and its reusable rules.

The finish review at `apps/client/.impeccable/review/finish-review.md` is
`disposition: ship` with no material fixes. The review names the three captures
as valid evidence: `welcome.png`, `workspace-light.png`, and
`workspace-dark.png`. The native direction and product contract are recorded in
`apps/client/.impeccable/surfaces/apps-client-crates-desktop.md` and
`docs/client-design.md`.

**Key Characteristics:**

- A fixed 240px show rail beside a spacious, scrollable working pane.
- A 56px quiet toolbar with account and reload actions.
- A 30px selected-show title, source field, and one clear sync hierarchy.
- Flat transfer rows with status, byte progress, throughput, and queue control.
- System light/dark appearance with zero-chroma structural surfaces.
- No fabricated show, episode, or transfer state in the welcome surface.

### Evidence and incumbent alignment

| Evidence | Shipped behavior recorded here |
| --- | --- |
| `src/tokens.rs` | Projects the root background, sheet, rail, ink, muted, border, divider, selection, action, and danger roles into GPUI Kit's theme. |
| `src/workspace.rs` | Implements the rail, toolbar, source form, sync actions, empty/welcome state, errors, and transfer rows described below. |
| `src/main.rs`, `src/platform.rs`, `src/quit.rs` | Supplies native window bounds, titlebar, system appearance observation, menu/status-item chrome, close-to-hide behavior, and explicit quit protection. |
| `.impeccable/review/*.png` | Confirms the topology, reading order, typography, material, light/dark ground, and live queue treatment. |

The finished surface matches the incumbent system in palette character,
system type, quiet borders, rounded controls, semantic states, and flat depth.
Its titlebar, macOS menu/status item, close-to-hide behavior, and keyboard quit
guard are native adaptations outside the headless content captures.

### Pre-existing drift, deliberately not repaired

- `tokens.rs` sets the global native font size to 14px. The root system's web
  body role is 16px; 14px is its field/label scale. This is the shipped dense
  Operate projection, so new native screens should follow it until the systems
  are deliberately reconciled.
- GPUI Kit receives one 10px theme radius, while the root system has a richer
  6/10/14/16/20/24px scale and capsule actions. Kit variants supply the rounded
  button treatment visible in the captures, but the native source does not
  project every web radius role.
- The native theme maps GPUI `muted` to the rail, `accent` to the selected row,
  and primary hover to the native ink/neutral values. These semantic mappings
  differ from the web `surface-muted`, `primary-soft`, and `primary-hover`
  roles, especially in dark appearance. They are documented as projection
  behavior rather than silently promoted to new root tokens.
- Native danger colors use direct GPUI RGB values (`#b44d42` and `#ef9a8d`),
  while the web authority stores its semantic danger ramp in OKLCH. The native
  values are retained as the implemented status treatment; no alternate root
  palette is introduced here.

## Colors

The native palette is a two-mode projection of Listenbox's achromatic web
roles. The frontmatter values are copied from `src/tokens.rs`'s semantic
lightness inputs and the incumbent root authority; `DESIGN.md` remains the
canonical source for cross-surface color changes.

### Primary

- **Charcoal action, light** (`{colors.action-light}`): the primary sync and
  sign-in action on a light ground.
- **Silver-charcoal action, dark** (`{colors.action-dark}`): the primary action
  on the dark ground, retaining light text.
- **Action ink** (`{colors.action-ink}`): readable text on either primary
  action.

### Neutral

- **Canvas** (`{colors.background-light}` / `{colors.background-dark}`): the
  main working ground.
- **Sheet** (`{colors.sheet-light}` / `{colors.sheet-dark}`): input and quiet
  control surfaces.
- **Rail** (`{colors.rail-light}` / `{colors.rail-dark}`): the fixed show and
  team navigation surface.
- **Ink** (`{colors.ink-light}` / `{colors.ink-dark}`): primary content and
  headings.
- **Muted copy** (`{colors.muted-light}` / `{colors.muted-dark}`): supporting
  descriptions, source names, queue status, and empty-state guidance.
- **Border** (`{colors.border-light}` / `{colors.border-dark}`): visible field
  and theme boundaries where a control needs a stronger edge.
- **Divider** (`{colors.divider-light}` / `{colors.divider-dark}`): toolbar,
  rail, transfer-row, and section separators.
- **Selected row** (`{colors.selected-light}` / `{colors.selected-dark}`): the
  selected show and quiet selected-control state.

### Semantic status

- **Danger, light** (`{colors.danger-light}`) and **danger, dark**
  (`{colors.danger-dark}`): errors and failed transfer state only. Status color
  is not decoration or a replacement for the charcoal action.

### Named Rules

**The Achromatic Ground Rule.** Structural surfaces stay neutral white, silver,
or charcoal. Do not add a warm cast, saturated navigation treatment, or
decorative accent to make an empty native pane feel designed.

**The Status-Only Color Rule.** Color enters the native workspace through a
semantic state such as danger. A new screen earns another color only by adding a
real product state and coordinating its role with the root authority.

**The Theme Follows the System Rule.** Resolve the palette through
`Tokens::current(cx)` and keep `Theme::sync_system_appearance` attached to the
window appearance observer. Never cache light colors in a screen.

## Typography

The desktop uses the operating system's sans face through GPUI Kit. It carries
Listenbox's native system typography without downloading a font or inventing a
display face. The 30px page-title and 18px title steps are explicit in the
native source; the 14px body default and 12px support text are visible in the
finished workspace and queue captures.

**Display Font:** the system sans face supplied by GPUI Kit

**Body Font:** the system sans face supplied by GPUI Kit

**Label/Mono Font:** no separate native label or mono face is introduced

**Character:** compact, legible, and quiet. Weight and scale create the
hierarchy; all-caps or decorative tracking are unnecessary for this Operate
surface.

### Hierarchy

- **Page title** (700, 30px): the selected show title and the welcome heading.
- **Title** (600–700, 18px): the Listenbox rail label and Transfers heading.
- **Body** (400, 14px): source values, action labels, descriptions, and the
  default workspace copy.
- **Label** (600, 14px): field labels and compact navigation/control emphasis.
- **Support** (400, 12px): source names, measured byte totals, and queue
  metadata. It must remain readable and never carry the main instruction.

### Named Rules

**The Native Type Rule.** Use the operating system sans through GPUI Kit and
the existing `tokens` projection. Do not load web fonts, use a display serif,
or add a second type family for a single native screen.

**The Useful Measure Rule.** Supporting copy may wrap in the working pane, but
the title, field, action row, and queue status must remain easy to scan at the
supported minimum window size.

## Layout

The native workspace is a fixed rail plus a flexible content pane. The rail is
240px wide and remains visible while the working pane scrolls. The default
window opens at 1080×760px and the implementation enforces an 840×600px
minimum. The toolbar is 56px high with 24px horizontal padding. The content
pane uses 24px padding and 24px section rhythm; controls in an action row use
12px gaps, while transfer rows use 8px internal gaps and 12px vertical
padding.

The first viewport follows one reading order: team picker and selected shows on
the left; toolbar/account actions at the top; selected-show title and source
configuration next; sync actions and current report next; the flat transfer
queue last. New screens should preserve this operational topology when they
extend the workspace. The content pane is allowed to scroll; the rail does not
become a second content canvas.

The implementation has no web breakpoint ladder. Window bounds, flex sizing,
and scroll ownership are the native responsive behavior. Keep the rail fixed,
let the content pane absorb width, and protect title/action rows with
`min_w_0`, `flex_1`, and explicit overflow ownership as the existing workspace
does.

### Named Rules

**The Stable Rail Rule.** The 240px navigation rail is a dependable coordinate
system for teams and shows. New navigation belongs inside its existing hierarchy
and must not compete with the selected-show work pane.

**The One Clear Action Rule.** A selected show exposes one primary operation at
each state. `Sync now` carries the primary treatment; watch, stop, save,
disconnect, pause, reload, and browser handoff remain secondary or stateful.

## Elevation & Depth

The native content surface is flat. Depth comes from the rail/canvas/sheet tone
steps, 1px borders and dividers, whitespace, and the selected-row fill. The
ordinary workspace has no ambient card shadow, gradient, texture, bevel, or
backdrop treatment. The operating system titlebar and status item are native
chrome and do not authorize decorative depth inside the content pane.

### Named Rules

**The Flat Transfer Rule.** Queue rows communicate progress with separators,
status copy, measured bytes, throughput, and the progress control. Do not lift
each transfer into a floating card.

**The Border Carries Depth Rule.** Use the projected border and divider roles to
mark a real boundary. If a region has no boundary or state change, let spacing
carry the separation.

## Shapes

The explicit native baseline is a 10px GPUI theme radius. GPUI Kit's button and
input variants apply their own native control treatment on top of that baseline;
the finished captures show soft team, field, and action corners. The transfer
queue stays row-shaped with bottom dividers rather than nested rounded cards.

Keep one clear silhouette per control. A selected show row may receive the
selected fill; an input may receive its sheet and boundary; the full workspace
does not become a stack of panels. If a future control needs a different radius,
first verify that the root system role and the GPUI Kit primitive require it.

## Components

### Buttons

- **Shape:** GPUI Kit button variants using the 10px native theme baseline and
  the kit's rendered soft action treatment.
- **Primary:** charcoal action with light text. Use `.primary()` for the one
  main operation in a region (`Sync now`, sign-in, or the welcome action).
- **Secondary / Ghost:** use the default or `.ghost()` variant for save,
  disconnect, watch, stop, reload, account, queue, and browser handoff actions.
- **Hover / Focus:** preserve GPUI Kit's state and focus treatment. Keep the
  button's position stable; state should be communicated by tone and label.
- **Disabled:** disable while loading, saving, stopping, plan-ineligible, or
  source-conflicted, as the existing workspace does. Do not replace a disabled
  action with an invented explanatory panel.

### Inputs / Fields

- **Style:** `Input::new` with the sheet surface, ink value, projected border,
  and the native control radius. The playlist field is full width in the
  selected-show pane.
- **Focus:** use the kit's native focus treatment and retain the workspace's
  focus ownership. Do not add a custom glow or shadow.
- **Disabled:** lock the field while a show is syncing, saving, stopping, plan
  blocked, or publishing to the mutually exclusive YouTube destination.

### Navigation

- **Rail:** fixed 240px surface with the Listenbox label, team picker, selected
  show list, and a concise source direction at the foot.
- **Selection:** show rows are ghost buttons whose selected row receives the
  selected semantic fill. Keep the text left-aligned and the list scrollable.
- **Appearance:** remap the complete rail, ink, sheet, divider, and selected
  roles when the operating system changes appearance.

### Cards / Containers

The workspace does not use ordinary cards. The main pane, source block, and
queue are flex regions separated by spacing and dividers. Add a container only
when it owns a real state or boundary, and prefer the existing sheet/rail
projection over a new surface color.

### Transfer Queue

The queue is the signature native component. Each row places the episode title
and source title on the left, phase/status on the right, and—when downloading—a
full-width progress bar followed by received/total bytes and measured MB/s.
Failed work uses the semantic danger role. The header keeps a queue pause/resume
control; an empty queue says where transfers will appear without fabricating a
job.

### Loading, empty, and recovery states

The welcome view asks the user to sign in and choose a podcast. The toolbar uses
the small native spinner while loading or handing off authorization. Errors are
short, actionable status copy. Stop, logout, and quit preserve the engine's
durable work and surface that work is draining or saved. New native screens must
represent empty, loading, authorization, partial failure, and stopped states
explicitly.

### Keyboard quit confirmation

The first ⌘Q press displays a compact HUD centered over the entire window's
content, without moving the rail, toolbar, or workspace. A 56px `⌘ Q` shortcut
sits above the instruction in the standard body role. The HUD uses the action
surface at 96% opacity, action ink, 24px padding, 12px spacing, a 280px width,
and the shared system's 20px radius. These dimensions live in `tokens.rs`.
There is no backdrop dimming, focus change, or full-width notification banner.
The hint stays readable for two seconds, then fades out over 200ms without
moving. Reduce Motion dismisses it without the fade. Its lifetime is independent
of key-up delivery, and a new press replaces the previous timer. macOS key state
confirms an actual hold or release, so a missed event cannot arm a stale quit.
During shutdown the same HUD shows "Saving progress" at the native title size
while admitted work drains. Light and dark captures live in the preview example
as `quit-light.png` and `quit-dark.png`.

## Do's and Don'ts

### Do:

- **Do** import and reuse `crate::tokens::{self, Tokens}` and resolve current
  roles through `Tokens::current(cx)`.
- **Do** keep the root `DESIGN.md`, dashboard, marketing, and transactional
  email roles as the cross-surface authority; this file is only their native
  projection.
- **Do** use GPUI Kit primitives (`Button`, `Input`, `Progress`, `Spinner`) and
  compose new screens from the 240/56/24/12/8/10 native scales recorded here.
- **Do** keep structural surfaces achromatic and make status color carry a real
  product state.
- **Do** preserve system appearance observation, visible operational states,
  truthful data, accessible progress labels, and stable scroll ownership.

### Don't:

- **Don't** copy Mazit's interface, palette, shadows, spacing, or control
  styling. Its transfer mechanics are an implementation reference only.
- **Don't** add gradients, textures, ambient shadows, decorative icons, or a
  second font to compensate for missing product content.
- **Don't** hardcode a one-off color, radius, or spacing scale in a screen when
  the native projection already supplies the role.
- **Don't** use platform colors as decoration; reserve them for a product
  identity or semantic state that is actually present.
- **Don't** render fabricated shows, episodes, queue rows, progress, or results
  in production states. Synthetic preview fixtures stay in the preview example.
- **Don't** turn the recorded projection drift into a new design rule. The
  native density, radius compression, and semantic mappings remain documented
  until a coordinated authority change updates the source and all consumers.
