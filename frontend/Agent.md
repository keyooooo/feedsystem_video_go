# ShortVideo frontend working agreement

Repository: https://github.com/keyooooo/feedsystem_video_go
Migration baseline: server_test / 58a7c0cd716e432925c886351cf8f368d6ab4b42
React migration source: frontend_refactor. Integrated full-stack base: origin/server_test.
For subsequent feature work, create a clean feature branch from the latest origin/server_test. Preserve uncommitted work in other worktrees. Backend and Compose changes require scope explicitly authorized by the user; this migration keeps their baseline content.

Each logical change must include new or updated behavioral tests. Run lint, typecheck, tests, browser regression and production build before delivery. Commit each validated batch separately, push the working branch and inspect CI (frontend_refactor/server_test pushes and pull requests run the full workflow). Never describe unexecuted checks as passing. Record blockers explicitly.

Use React 19, TypeScript, Vite 7, React Router 7, Zustand 5 and CSS Modules. Keep server APIs and token keys compatible. No credentials or test account passwords in tracked files. API proxy target: http://8.134.84.151:8080.

Design: background #0B0D12, panels #151922, text #F3F5F7, muted #A7AFBE, primary #5EEAD4, liked #FB7185. Native semantic controls, Radix overlays, Lucide icons, 44px targets, reduced motion. Test mobile and desktop, keyboard and failure states.
