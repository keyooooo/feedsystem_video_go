# ShortVideo frontend working agreement

Repository: https://github.com/keyooooo/feedsystem_video_go
Base: server_test / 58a7c0cd716e432925c886351cf8f368d6ab4b42
Working branch: frontend_refactor. Preserve backend and Compose configuration.

Each logical change must include new or updated behavioral tests. Run lint, typecheck, tests, browser regression and production build before delivery. Commit each validated batch separately, push to origin/frontend_refactor and inspect CI. Never describe unexecuted checks as passing. Record blockers explicitly.

Use React 19, TypeScript, Vite 7, React Router 7, Zustand 5 and CSS Modules. Keep server APIs and token keys compatible. No credentials or test account passwords in tracked files. API proxy target: http://8.134.84.151:8080.

Design: background #0B0D12, panels #151922, text #F3F5F7, muted #A7AFBE, primary #5EEAD4, liked #FB7185. Native semantic controls, Radix overlays, Lucide icons, 44px targets, reduced motion. Test mobile and desktop, keyboard and failure states.
