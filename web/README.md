# autoskills web

Review dashboard for AutoSkills — suggested skills distilled from AI coding transcripts, reviewed as accept / edit / reject.

## stack

Vite + React + TypeScript + SCSS (BEM, per-component sibling `.scss` files, `src/styles/globals.scss` imported once in `main.tsx`). No UI libraries, no Tailwind. The design language is warm monochrome: a single ink color `#2a2119`, Instrument Sans / LT Superior Serif / Geist Mono, pill buttons, mono uppercase labels, and no shadows or accent colors.

## dev workflow

The dashboard talks to the AutoSkills Go daemon. In dev, Vite proxies `/api` to `http://127.0.0.1:4517` (see `vite.config.ts`), so start the daemon first:

```bash
autoskills serve          # or however you run the Go binary, listening on 127.0.0.1:4517
```

then:

```bash
pnpm install
pnpm dev                  # http://localhost:5173
```

Without the daemon running, the UI loads but shows a "daemon unreachable" state. For UI work without the daemon, a tiny mock of the API is included:

```bash
node mock-api.mjs           # serves fixture data on 127.0.0.1:4517
```

## build

```bash
pnpm build                # tsc + vite build -> dist/
```

In production the Go binary serves `dist/` and the API from the same origin, so no proxy is involved.

## fonts

Local fonts live in `public/fonts/` (Geist Mono Variable, LT Superior Serif, Raveo Display); Instrument Sans is loaded from Google Fonts in `globals.scss`.
