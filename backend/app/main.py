from contextlib import asynccontextmanager
import logging

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from .config import get_settings
from .routes.ai_blocks import router as ai_blocks_router
from .routes.image_generation import router as image_router
from .routes.text_expansion import router as text_router


logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s %(levelname)s %(name)s %(message)s',
)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    # Do not fail imports or tests without a key; the production entrypoint
    # performs an explicit check below before serving real requests.
    yield


app = FastAPI(title='AI Scratch Web API', version='0.1.0', lifespan=lifespan)
app.add_middleware(
    CORSMiddleware,
    allow_origins=['http://127.0.0.1:5173', 'http://localhost:5173'],
    allow_methods=['POST', 'GET'],
    allow_headers=['Content-Type'],
)
app.include_router(image_router)
app.include_router(text_router)
app.include_router(ai_blocks_router)


@app.middleware('http')
async def limit_image_request_size(request, call_next):
    max_bytes = None
    if request.url.path.startswith('/api/image/'):
        max_bytes = get_settings().image_max_request_bytes
    elif request.url.path.startswith('/api/ai-blocks/'):
        max_bytes = get_settings().ai_blocks_max_request_bytes
    if max_bytes is not None:
        content_length = request.headers.get('content-length')
        if content_length:
            try:
                too_large = int(content_length) > max_bytes
            except ValueError:
                too_large = True
            if too_large:
                return JSONResponse(
                    status_code=413,
                    content={'detail': {'code': 'REQUEST_TOO_LARGE', 'message': '请求内容过大'}},
                )
        elif request.url.path.startswith('/api/ai-blocks/'):
            body = await request.body()
            if len(body) > max_bytes:
                return JSONResponse(
                    status_code=413,
                    content={'detail': {'code': 'REQUEST_TOO_LARGE', 'message': '请求内容过大'}},
                )
    return await call_next(request)


@app.get('/health')
async def health():
    settings = get_settings()
    return {
        'status': 'ok',
        'provider_configured': bool(settings.sui_xiang_api_key),
        'agnes_configured': bool(settings.agnes_api_key),
    }


if __name__ == '__main__':
    settings = get_settings()
    settings.require_api_key()
    import uvicorn
    uvicorn.run('app.main:app', host='127.0.0.1', port=8000, reload=False)
