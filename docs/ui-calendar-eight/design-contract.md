# Ky suite design contract

These are the targets for KyPost, KyCalendar, KyMessages (console and MemberHome) and any other
Ky web product. They describe one calm, operational design language: neutral cream or charcoal
surfaces, with orange used sparingly.

## Ownership

- **Shared runtime values live in ky-ui** (`ky-ui/src/tokens.css`). These are colors, font stacks,
  radii, the field border, and the type and space scales. When a value below has no
  `--ky-*` token yet, add the token to ky-ui in its own change and sync it to consumers. A product
  never defines a second copy of a shared value.
- **Geometry and composition stay in each product.** That includes page layout, sidebar
  structure, routes and components. Products consume tokens through their compatibility aliases.
- **The palette is not copied.** Read it from `tokens.css`. Generated copies (`palettes.ts`,
  `palettes-browser.js`, KyMessages' `element.json.tmpl`) must be regenerated or rechecked when
  tokens change. They are never hand-tuned.

## Foundations

| Area | Contract |
|---|---|
| Typeface | Space Grotesk, already self-hosted by each product. IBM Plex Mono only for code, keys and fixed-width data. No remote font hosts (CSP). |
| Type sizes | Page title 24px/600, one `h1` per page in `--ky-ink-strong`. Section heading 17px/600. Body 15px. Navigation 14px. Meta and labels 13px. Line height 1.4–1.5 for body text. |
| Headings | One title, with optional plain section headings. No eyebrows, no letter-spaced mono labels, no stacked eyebrow + section + title. |
| Rhythm | 8px base (8, 16, 24, 32). 4px only inside a control or between an icon and its label. |
| Controls | 40px height for buttons, inputs and selects; 8px radius. |
| Surfaces | `--ky-bg` page, `--ky-panel` content. Quiet `--ky-line` dividers. Content sits flat on one surface: no card inside a card, no box around every field. Shadow only on overlays (menus, dialogs, toasts). |
| Field borders | ≥ 3:1 against their surface in every theme. Consume `--ky-field-border` from ky-ui 0.3.0; quiet dividers are not field borders. |
| Focus | Visible focus on every interactive element: the shared 2px accent outline with a 2px offset, or the browser's visible native indicator inside native controls. |

## Color roles

- **Orange (`--ky-accent`) is reserved for** the single primary action of a view, the selected
  navigation item or selected state, and focus. It is not used for decoration, avatars,
  headings, counts or idle toolbars.
- **Buttons** come in four kinds. Primary is filled accent with `--ky-button-text`.
  Secondary is an outline in ink. Tertiary is text only. Destructive uses `--ky-danger` as an
  outline or text, set apart from Save and Send, and confirmed where the loss is irreversible.
  A disabled button must look distinct from an enabled secondary one, not just be a faded primary.
- **Status colors** are `--ky-success`, `--ky-warning` and `--ky-danger`, always paired with text
  or an icon. Other hues appear only as user data, such as calendar colors or user-chosen labels.

## Shell

- **KyCalendar uses one left navigation region.** Calendar selection and management must not form a second panel beside the app sidebar; the owner rejects that double-panel composition.
- **Sidebar.** 240px on desktop. Below the product breakpoint, keep the existing accessible responsive pattern
  (drawer or horizontally scrollable navigation), with the same order and labels.
- **Header.** Product wordmark and the Busnes stamp icon at one size across products.
- **Navigation.** Rows are 14px with an icon and label, 40px tall, with an 8px radius. The
  selected row uses the ky-ui treatment: a surface change plus an accent rail. Section
  expanders use chevrons.
- **Account region.** Pinned to the bottom of the sidebar. It shows the signed-in name, the
  role where the product has one, and sign-out. Keep the theme choice discoverable in Settings/Appearance or the existing account
  region; preserve the product adapter and saved choice.
- **Page header.** `h1` title on the left and the single primary action on the right. Secondary
  actions are quiet.

## Task views

- **List rows** are full-bleed and divided by `--ky-line`. Unread or active rows differ by weight, not by color fill.
- **Bulk-action toolbars** are contextual. Actions appear when there is a selection, as quiet
  icon-and-label buttons.
- **Empty states** say what belongs here and offer the next action. **Loading** uses skeletons in
  place. **Errors** state what failed and how to recover.

## Forms and dialogs

- **Fields.** Labels sit above plain inputs, in titled groups. Required and optional are marked in text.
- **Validation.** Errors appear inline under the field, in text plus `--ky-danger`, linked with
  `aria-describedby`. Server errors are summarised at the top of the form.
- **Dialogs.** Use the native `<dialog>` or equivalent focus handling. The title is the action
  ("Delete contact?"), primary on the right, cancel beside it. Focus returns to the trigger.

## Preserve

- Named themes and saved theme choices, with each product's existing storage key and adapter.
  The OS-following default stays the same.
- Roles and authorization. Admin-only controls stay hidden from members.
- CSP (`script-src 'self'`, no inline script) and existing security defaults.
- Routes, behavior and factual copy. Ask before changing a feature or adding a claim.

## Historical baseline notes

These describe the pre-redesign sources in the 2026-10-26 council
(`reviews/2026-10-26-council/source-evidence.md`), not the current implementation.
Read the product implementation records and current source before editing.

- **KyPost.** Every page sits in a shadowed `.panel` card. Every `button` is filled accent.
  Inputs use the 22%-alpha line. Page titles are unstyled `h2`. Sign-out is a nav link.
- **KyCalendar.** It has the fullest local house style (space, radius and field-border tokens,
  and button variants). These are the source for the upstream token proposal. The workspace
  `h1` renders in muted ink.
- **KyMessages.** The console and `MemberHome` use the older ky_server_base theme, with only
  primary and secondary buttons. Element chat stays stock and is themed only via `config.json`.
  Element's system-theme setting and typeface need their own capture and supported
  configuration checks; they are not proven by console screenshots.
- **Consumers.** KyCalendar and KyMessages vendor ky-ui but are missing from `consumers.json`.
  A token release does not reach them until they are added or synced by hand.
