# Piko design direction

Reading this as: a Persian-first product workspace for small business owners, with a joyful, artistic but professional visual language, leaning toward a clean accessible component foundation and a coral-led palette.

Greenfield, standalone frontend. The requested design-taste-frontend skill informed the brand, type, palette, restrained motion and quality checks. Its marketing-specific hero, section-composition and headline rules are not applicable to this dashboard and multi-step product UI. The product controls use the official Radix Themes foundation.

DESIGN_VARIANCE: 6
MOTION_INTENSITY: 4
VISUAL_DENSITY: 5

## Design rules

- One primary action color: coral.
- Supporting colors serve illustrations, semantics and analytics, following the user's joyful-color direction.
- Surface radii: 20px major branded panels, 16px product panels, 12px components, 8px compact controls. Avatar circles express identity, and pills express status.
- RTL by default. English identifiers and secret-like demo token fields explicitly use LTR.
- Light and dark tokens apply at the page root. No inverted sections. Dark mode uses charcoal and slate surfaces with neutral gray Radix controls, following the approved palette refinement.
- Local illustrative interactions only. No backend, accounts, credentials, payment or deployment integrations.
- Loading skeletons in chat, empty search state, inline connection-form errors, button feedback, focus states and reduced-motion fallbacks.

## Generated assets

Built-in image_gen was used for the logo, its revision and the companion illustration. Original images were copied to `public/assets`, with no manual image edits. Responsive WebP copies preserve alpha and reduce transfer size; the originals are retained.

### piko-mark.png (original concept, retained)

Use case: logo-brand. Create a professional and joyful logo symbol for a Persian-first AI Telegram bot builder. Brand name is Piko, Persian پیکو, inspired by peik/messenger. Only the SYMBOL, no typography. A distinctive sculptural flat graphic combining a folded messenger bird / paper plane with a rounded conversation loop, gently asymmetrical, three interlocking rounded coral ribbon shapes with a small deep aubergine central negative space. Palette dominant coral #ed6a52, supporting peach #ffd1b8 and deep aubergine #332c40. Warm playful energy but strong simple silhouette readable at 32px. Clean vector-style edges, no gradients, no glow, no shadows, no mockup, no surrounding decoration. Centered single mark filling 75% of square canvas. Actual transparent background.

### piko-companion.png

Use case: stylized-concept. Asset type: friendly editorial illustration for a Persian business bot-builder dashboard called Piko. Wide landscape 3:2 composition on genuinely transparent background. A joyful sculptural pale lilac 3D retro-futuristic chatbot mailbox with a friendly dark aubergine face and two small rounded eyes, wearing a folded coral paper messenger bird as its companion, sitting on a wavy abstract pale peach pedestal. A mint green rounded chat tile and one apricot abstract little spark hover nearby. Beautiful tactile matte clay / paper material, soft studio light, quiet sophisticated boutique software illustration, purposeful and minimal, rounded geometric design, dominant pastel lavender #ded6ef, coral #ed6a52, mint #b9d9cc, pale peach #f9d0b7. View 3/4 front perspective. Object arranged as one cohesive sculpture filling image, no text, no letters, no logos, no UI mockup, no gradient background. Silhouette reads clearly at 240px. More art-directed sculpture than generic robot. Transparent background, no floor.

### Active logo and favicon revision

`piko-mark-v2.png` is the generated transparent master; `piko-mark-v2.webp` is the app asset. Matching 16px, 32px and 180px PNGs and a multi-size ICO use the same mark. The logo is a coral P with a speech-bubble counter. Complete generation and refinement prompts are in `LOGO-PROMPTS.md`.
