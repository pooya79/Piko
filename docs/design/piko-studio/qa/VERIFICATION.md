# Frontend verification

- Production build: passed (`npm run build`).
- Browser smoke checks: 20 desktop and mobile screenshots, including light/dark modes, with no page errors, broken images, or document overflow.
- Verified interactions: sidebar navigation, theme changes, bot search and empty state, bot detail tabs, flow node selection, chat loading and local replies, simulated publishing, Telegram preview buttons, onboarding error feedback and example prompts, billing dialog, mobile chat preview.
- Explicitly checked: dark mode changes computed page colors and the Radix date selector preserves its official 32px control height.
- Lighthouse mobile simulation: performance 77, accessibility 100, best practices 100. LCP 5.3s under throttling, CLS 0, total blocking time 50ms. This is a visual prototype; the mobile LCP does not meet the 2.5s production target.
- All data, analytics, testing, publishing and payments are illustrative. No backend integrations are present.

Screenshots and raw audit output are saved beside this file. Run `node qa/browser-check.mjs` with the preview running at port 4174 to repeat the browser checks. The script uses `/usr/bin/google-chrome`; adjust the executable path for a different machine.
