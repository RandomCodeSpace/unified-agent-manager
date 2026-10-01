---
name: uam-design
description: Design defaults for web and UI work. Load before you build or restyle a page, component, dashboard, landing page, prototype or mockup, or propose design options. The owner's own instructions and design skills, and the project's design system, come first; this skill fills only what they leave open.
---

# uam-design

Defaults that make interface work look deliberate on any model. They are the
**fallback layer**: every rule here yields to the owner and the project.

## 1. Find the owner's design first

Read these in order. A higher source wins wherever two disagree:

1. What the owner asked for in this conversation.
2. The owner's instructions and skills about design: `AGENTS.md`,
   `CLAUDE.md`, `.github/copilot-instructions.md`, and any other loaded
   skill about design, frontend or UI.
3. The project's design system: a `DESIGN.md` or style guide, theme and token
   files (CSS custom properties, the Tailwind theme), the component library
   in use, and the existing screens.

Done when you can name the source for colour, type, spacing and components,
or know that one is open. Apply section 3 only to what is open, and list in
your reply which defaults you used, so the owner can overrule them.

## 2. Workflow

1. **Purpose.** Name who uses the screen, its one primary task, and the data
   it shows. Use realistic data, taken from the project when it has some.
2. **Direction.** Commit to one direction in a sentence: density, tone,
   character. When asked for options, ideas or a prototype, build three
   variants that differ in **structure** (layout, hierarchy, primary action),
   switchable on one route with `?variant=`. A prototype is throwaway code:
   named as a prototype, in-memory data, no persistence.
3. **Build** with the project's stack and components.
4. **Look at it.** When a headless browser is available (Playwright,
   Chrome), screenshot 1440×900 and 390×844, open the images, and fix what
   you see. Done when neither size shows overflow, clipped or wrapped labels,
   overlapping parts or a broken empty state. Show the owner the screenshots
   with `uam_show_file`.

## 3. Defaults for what is open

**Layout**
- App screens use the full viewport: sidebars, columns and grids. Cap only
  prose, at about 75 characters per line, inside its column.
- One primary action per screen. Build hierarchy with size, weight and
  position, then colour.
- Spacing comes from one scale: 4, 8, 12, 16, 24, 32, 48, 64 px. Text is
  left-aligned; centre only a short heading.
- Group with spacing and headings. A card marks one distinct object.

**Type**
- One sans family (the system stack or one quality webfont), plus a mono for
  code. Scale: 12, 13, 14, 16, 20, 24, 32 px. Body 14–16 px, line height
  1.4–1.6. Numbers in tables use tabular figures.

**Colour**
- A neutral ramp, one accent used sparingly, and semantic colours for
  success, warning, error and info. Define each once as a token and use only
  tokens.
- Text contrast at least 4.5:1, UI parts and focus rings 3:1. Pair every
  colour signal with text or an icon.

**Surfaces and icons**
- Depth comes from a background step or a soft shadow. Use two radii: a small
  one for controls and a larger one for panels.
- Icons come from one set (Lucide, for example) at one stroke width.

**Components and states**
- Use the project's library. Without one, use native elements or accessible
  primitives such as Radix, Base UI or shadcn/ui.
- Build every state: hover, visible keyboard focus, active, disabled,
  loading (a skeleton in the final shape), empty (what goes here, plus the
  action that fills it), and error (what happened, plus the next step).

**Copy**
- Labels are verbs that name the result ("Save changes", "Start task"), in
  sentence case. Status reads as a plain sentence.

**Motion and performance**
- Transitions run 150–250 ms and animate `transform` and `opacity`. Honour
  `prefers-reduced-motion`.
- Large areas use solid fills. Size images, lazy-load heavy assets, and
  reserve space so nothing shifts as it loads.

**Responsive and accessible**
- Works from 360 px wide with no sideways page scroll; touch targets at least
  44 px.
- Semantic HTML, a label for every control, and a complete keyboard path.

## 4. Before you reply

Check every item:

- The primary action is obvious within three seconds of looking.
- Every colour and size comes from a token or the scale.
- Contrast meets the ratios in section 3.
- Each state in section 3 exists, or you state why one cannot occur.
- Both screenshot sizes are clean, or you say that no browser was available.
- The reply names the defaults you applied and the sources you followed.
