# Frontend (UI)

This directory contains the static frontend (HTML, JS, and CSS) for the `netsim-in-a-box` tool.

## Frontend Architecture

To keep the tool lightweight, secure, and free from complex build dependencies in Docker, we **pre-compile** our production CSS file (`production.css`) using the **Tailwind CLI**.

We do not use the Tailwind CDN in production. We check-in the generated production.css file directly into the repository.

## Arquivos-Fonte

* `index.html`: The main UI structure.
* `app.js`: The vanilla JavaScript that consumes the backend API.
* `package.json`: Defines development dependencies (`tailwindcss`) and the build script.
* `tailwind.config.js`: The Tailwind configuration.
* `input.css`: The source file that Tailwind uses to generate the CSS.
* `production.css`: **(GENERATED FILE)** This is the final, minified CSS file used by the application.

---

## How to Update Styles (CSS)

If you make any style changes in `index.html` or `app.js` (by adding new Tailwind classes), you must "recompile" the production.css file locally.

### Install Dependencies

(You only need to do this once)

```bash
# Navigate to this directory (ui)
cd frontend

# Install tailwindcss CLI
npm install
```

### Generate Production CSS

Run the build script defined in `package.json`:

```bash
# This will read input.css and generate the minified production.css
npm run build:css
```
