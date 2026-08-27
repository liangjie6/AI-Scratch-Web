# AI Scratch Web Architecture

The browser owns the Vue layout, the locally bundled Scratch GUI/VM, and the
generated-image preview/import interaction. The VM is the source of truth for
targets and assets: generated images are resized into PNG assets, then added
through `addBackdrop` or `addSprite`, so `saveProjectSb3()` persists them.

The FastAPI service owns the only provider boundary. `POST /api/image/generate`
validates the Chinese prompt, style, canvas pair, target, and fixed `1x1`
layout. The Sui Xiang adapter uses `https://sui-xiang.com/v1`, model
`gpt-image-2`, and the OpenAI-compatible `images.generate` call. Provider
responses are downloaded or decoded server-side, checked as images, normalized
to PNG, and returned as base64 to the local browser.

The Vite development server listens on all network interfaces and proxies `/api` to `127.0.0.1:8000`. Scratch GUI,
fonts, libraries, block media, and the default project are served from
`frontend/public/assets/scratch`; no Scratch CDN or platform account is needed
for editing, running, or SB3 read/write.
