# React migration and product redesign

Approved scope: preserve backend protocol and redesign the ShortVideo consumer interface.

1. Rules, baseline tests and CI.
2. Design system: charcoal, mint, coral; system typography; responsive shell and accessible components.
3. Framework independent API and session store, concurrent refresh and logout race protection.
4. Navigation, authentication and settings.
5. Windowed feed, hot ranking, detail and comments.
6. Profiles, social and messages.
7. Resumable three-worker, 5MB chunk uploads, three attempts, cancellation.
8. React cutover, remove Vue, visual and browser verification, Docker CI and live server tests.

Each batch carries tests and a separate commit. Verification: lint, typecheck, unit/component tests, Playwright Chromium/WebKit, production build, container routing/proxy/Range smoke tests and dedicated-account live integration. Capture 390x844, 768x1024, 1440x900 and check 360px overflow. Production replacement is a later action.

Retain all existing URLs and paging contracts. React preview runs with Vite react mode until cutover. Default /api and /static proxy to configurable API_PROXY_TARGET. Do not edit deployed backend or middleware. Dedicated test credentials remain in ignored local files.

## Progress
- Worktree created from approved baseline.
- Foundation commit: 09ce57e; original Vue build and four baseline tests passed.
- React preview foundation: five tests, both type checks and both production builds passed.
- Remote blocker: configured credential helper is absent; GitHub connector write request returned HTTP 403. Local work can continue; remote CI remains unverified.
- Shared session and request layer implemented with regression tests for concurrent refresh and logout races.
