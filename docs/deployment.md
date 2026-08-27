# Local Deployment

## One-click startup (Windows)

Double-click `start-all.bat` in the project root to start the backend and frontend in separate command windows. The frontend listens on all network interfaces, while the backend remains available only through the frontend's local `/api` proxy. The launcher checks local dependencies, skips services whose ports are already listening, and opens `http://127.0.0.1:5173/` on the host computer. Other devices on the same LAN can open `http://<host-LAN-IP>:5173/`. Close the two service windows to stop them.

The backend reads `backend/.env` when present. Copy `backend/.env.example` to `backend/.env`, set `SUI_XIANG_API_KEY` for image generation, and set `AGNES_API_KEY` for streaming text expansion and AI Scratch-block generation.

## Backend

```powershell
cd D:\ywj\AI-Scratch-web
python -m venv backend\.venv
backend\.venv\Scripts\python.exe -m pip install -e ".\backend[test]"
$env:SUI_XIANG_API_KEY = "..."
$env:AGNES_API_KEY = "..."
cd backend
.\.venv\Scripts\python.exe -m app.main
```

Both keys are read only by the backend. `/health` reports image and Agnes configuration separately, while an unconfigured provider returns a clear error only when its feature is used. Do not put either key in Vue, Vite, browser storage, or an SB3 file.

## Frontend

```powershell
cd D:\ywj\AI-Scratch-web\frontend
npm install
npm run dev
```

Open `http://127.0.0.1:5173/` on the host computer, or `http://<host-LAN-IP>:5173/` from another device on the same LAN. Run `ipconfig` to find the host's IPv4 address. The Vite development and preview servers listen on `0.0.0.0`; their `/api` proxies use `http://127.0.0.1:8000` by default. Set `AI_SCRATCH_API_ORIGIN` before `npm run dev` or `npm run preview` to point at another backend. `npm run prepare:scratch` refreshes the bundled Scratch GUI assets after an aligned `@scratch/*` package update. If another device cannot connect, allow inbound TCP ports `5173` (development) or `4173` (preview) in Windows Firewall.

## Verification

```powershell
cd D:\ywj\AI-Scratch-web
backend\.venv\Scripts\python.exe -m pytest backend/tests -q
npm --prefix frontend run test:unit
npm --prefix frontend run build
npm --prefix frontend run test:e2e
```

The browser talks only to local `/api/image/generate`, `/api/text/expand`, and `/api/ai-blocks/*` endpoints. Real image generation requires the Sui Xiang key; text expansion and AI block clarification, planning, and ProgramIR generation use Agnes with the `agnes-2.5-flash` model. AI block provider calls time out after 120 seconds by default; override this with `AI_BLOCKS_PROVIDER_TIMEOUT_SECONDS` when needed. Playwright mocks provider endpoints for deterministic tests while still exercising the real Scratch VM integration.
