# FreeCloud

## Register

product

## Platform

web

## Users

IT administrators, homelab operators, and small-to-medium IT teams managing a fleet of devices and a directory of users through Keycloak (IdP/SSO/SCIM) and FleetDM (device management/MDM). They are technical operators who understand identity and device management — they don't need hand-holding, but they do need clarity under pressure (offboarding a user, investigating a compliance failure).

Primary context: seated at a desktop or laptop, multi-monitor, managing 10-10,000 users/devices. Speed of task completion matters more than visual delight.

## Product Purpose

FreeCloud is a single pane of glass over Keycloak and FleetDM — an open-source alternative to JumpCloud. It unifies employee onboarding/offboarding, device compliance checks, SSO application management, and audit into one interface that speaks the language of both IdP and MDM without requiring the admin to context-switch between Keycloak's admin console and FleetDM's dashboard.

Success: an admin can onboard a user (create in Keycloak + provision Fleet enrollment token) in one click, offboard them with device wipe in another, and see the full audit trail of every action without leaving the dashboard.

## Brand Personality

**Technical, trustworthy, restrained.**

- Technical: respects the admin's expertise. Doesn't explain what a JWT or a SCIM provisioning connector is. Focuses on the action, not the onboarding copy.
- Trustworthy: color palette is deliberate, not decorative. Actions have clear consequences. Failure states are explicit, not swallowed.
- Restrained: indigo accent on slate neutrals. One font (Inter). No glassmorphism, no gradient text, no bounce animations. The tool should disappear into the task.

## Anti-references

- **JumpCloud / Okta admin panels**: cluttered, inconsistent component vocabulary, buried actions, overwhelming density for small deployments.
- **"SaaS cream" templates**: the indigo + slate + gradient + hero-metric template that dominates AI-generated dashboards. FreeCloud uses indigo + slate deliberately but avoids all the decorative tells (side-stripe borders, glass cards, gradient icons, tiny uppercase eyebrows).
- **Over-designed admin UIs**: anything where visual flair competes with task speed. Motion conveys state, not decoration.

## Design Principles

1. **The tool disappears into the task.** Every visual decision is tested against "does this help the admin do their job faster?" If it doesn't, it's removed. Content density over whitespace-for-elegance. 150-250ms transitions, no orchestrated page-load animations.

2. **Security is visible, not decorative.** Warnings are amber, errors are red, success is green. Device policy badges, compliance scores, and audit chain integrity status use the same semantic vocabulary across every screen. Color carries consistent meaning — never decoration.

3. **Consistency earns trust.** Same button shape (rounded-lg, px-4 py-2.5), same form control vocabulary, same icon library (lucide-react), same heading pattern (title + subtitle → error banner → search/filter → content) across every page. Familiarity is a feature for an admin tool.

4. **Every state matters.** Loading uses skeleton rows, not spinners. Empty states teach the interface. Error messages are human-readable. Offboarding partial-success surfaces warnings alongside results. No silent failures, no blank pages.

5. **Failure is explicit, not silent.** When FleetDM is unreachable during onboarding, the response is 202 Accepted with a visible warning — not a silently passed check. When device wipe fails for one of two devices, both the wiped and failed counts are reported. When audit chain integrity is broken, the admin sees the exact sequence number.

## Accessibility & Inclusion

- WCAG AA minimum (4.5:1 body text, 3:1 large text/UI components)
- Skip-to-content link present
- Focus-visible rings on all interactive elements (2px indigo + 2px offset)
- Dark mode with `prefers-color-scheme` auto-detection + manual toggle
- Color is never the only indicator (icons + labels alongside status colors)
- Gradient text, glassmorphism, and bounce easing are banned
