# پیکو | Piko

A standalone, Persian-first frontend design concept for an AI Telegram business bot builder and maintainer. All activity, analytics, subscriptions, tests, deployments, and chat replies are illustrative local demo data. No Telegram token is transmitted and no payment or deployment is performed.

## Archived reference

Copied on 2026-10-03 from `/home/mint/oss/test-ui/piko-studio` for future work on this repository. This is a standalone design reference; it is not wired into the Go application.

The snapshot preserves React/CSS source, build configuration and dependency lockfile, design notes and logo prompts, original PNGs and WebP assets, favicon exports, fonts and their licenses, and the existing QA screenshots and reports. `node_modules/` and generated `dist/` output are excluded. QA results describe the original preview run, not a new verification of this copy.

The active logo master is `public/assets/piko-mark-v2.png`; the web version is `public/assets/piko-mark-v2.webp`. Earlier logo concepts are retained alongside it. Browse `qa/` for desktop, mobile, light and dark reference screenshots.

## Run

From the repository root:

```bash
cd docs/design/piko-studio
npm ci
npm run dev
```

## Views

- `/#home`: Account overview, describe-a-bot prompt, metrics, activity and recent bots.
- `/#chat`: Conversational maintenance, build/test summary and interactive Telegram preview.
- `/#bots`: Bot collection with search, filters and empty state.
- `/#detail`: Bot overview, flow graph, individual analytics and settings.
- `/#analytics`: Account analytics and per-bot comparison.
- `/#create`: BotFather introduction, token connection and idea description.
- `/#billing`: Subscription, usage and example invoices.

## Design

Piko takes its name from the Persian word پیک, a messenger. The generated coral P monogram combines the initial with a conversation bubble. Matching favicon exports use the same mark. The supporting clay illustration carries the same coral, lilac, peach and mint palette.

The interface uses Vazirmatn for Persian and Manrope for Latin identifiers, both self-hosted. Coral is the primary action color; supporting pastels identify bot types, successful outcomes and data series. Semantic tokens support system dark mode and a manual theme toggle. Layout is RTL throughout, with LTR islands for handles, tokens and charts where appropriate.

Built with React, Vite, the official Radix Themes package, Phosphor icons and Motion. Product layouts are original compositions; Radix supplies accessible controls and dialog primitives. Page transition motion acknowledges navigation and respects reduced motion. See `DESIGN.md` for the art direction and generation prompts.

## Build

```bash
npm run build
npm run preview
```

All source and assets are contained in this directory. The existing project was not inspected or reused.
