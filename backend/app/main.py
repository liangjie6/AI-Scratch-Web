from contextlib import asynccontextmanager
import logging
import os
from typing import Annotated

from fastapi import FastAPI, Header
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from .config import get_settings
from .routes.ai_blocks import router as ai_blocks_router
from .routes.ai_runs import get_ai_run_manager, get_ai_run_store, router as ai_runs_router
from .routes.image_generation import router as image_router
from .routes.text_expansion import router as text_router


_configured_log_level = getattr(logging, os.getenv('AI_SCRATCH_LOG_LEVEL', 'INFO').upper(), logging.INFO)
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s %(levelname)s %(name)s %(message)s',
    force=True,
)
# AI_SCRATCH_LOG_LEVEL controls application business-flow diagnostics only.
# Transport clients stay quiet even during local DEBUG sessions.
logging.getLogger('app').setLevel(_configured_log_level)
logging.getLogger('httpcore').setLevel(logging.WARNING)
logging.getLogger('httpx').setLevel(logging.WARNING)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    # Do not fail imports or tests without a key; the production entrypoint
    # performs an explicit check below before serving real requests.
    store = get_ai_run_store()
    interrupted = store.fail_incomplete_runs()
    if interrupted:
        logging.getLogger(__name__).warning('marked_interrupted_ai_runs count=%d', interrupted)
    store.purge_expired()
    try:
        yield
    finally:
        await get_ai_run_manager().shutdown()
        store.close()


app = FastAPI(title='AI Scratch Web API', version='0.1.0', lifespan=lifespan)
app.add_middleware(
    CORSMiddleware,
    allow_origins=['http://127.0.0.1:5173', 'http://localhost:5173'],
    allow_methods=['POST', 'GET'],
    allow_headers=['Content-Type', 'X-AI-Run-Scope', 'Last-Event-ID'],
)
app.include_router(image_router)
app.include_router(text_router)
app.include_router(ai_blocks_router)
app.include_router(ai_runs_router)


@app.middleware('http')
async def limit_image_request_size(request, call_next):
    max_bytes = None
    if request.url.path.startswith('/api/image/'):
        max_bytes = get_settings().image_max_request_bytes
    elif request.url.path.startswith(('/api/ai-blocks/', '/api/ai-runs')):
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
        elif request.url.path.startswith(('/api/ai-blocks/', '/api/ai-runs')):
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
        'deepseek_configured': bool(settings.deepseek_api_key),
        'gpt_configured': bool(settings.gpt_api_key),
    }


@app.get('/api/capabilities')
async def capabilities(
    x_ai_run_scope: Annotated[str | None, Header(alias='X-AI-Run-Scope')] = None,
):
    """Deployment capabilities only; never expose prompt, project, or provider data."""
    settings = get_settings()
    rollout_scope = x_ai_run_scope.strip() if x_ai_run_scope and len(x_ai_run_scope) <= 512 else ''
    durable_runs_available = settings.durable_runs_enabled_for_scope(rollout_scope) if rollout_scope else (
        settings.ai_durable_runs_enabled and settings.ai_durable_runs_rollout_percent >= 100
    )
    return {
        'ai_blocks_protocol': [2, 3],
        'ai_runs_protocol': 'ai-scratch-run/1',
        'project_manifest': [2, 3],
        'program_ir': [1, 2],
        'features': {
            'ai_commit': settings.ai_blocks_commit_enabled,
            'project_assets': settings.ai_project_assets_enabled,
            'sound_assets': settings.ai_sound_assets_enabled,
            'audio_generation': settings.ai_audio_generation_enabled,
            'asset_auto_match': settings.ai_asset_auto_match_enabled,
            'asset_auto_match_min_score': settings.ai_asset_auto_match_min_score,
            'asset_auto_match_min_margin': settings.ai_asset_auto_match_min_margin,
            'destructive_sprite_ops': settings.ai_destructive_resource_ops_enabled,
            'destructive_sound_ops': settings.ai_destructive_resource_ops_enabled,
            'logical_resource_refs': settings.ai_logical_resource_refs_enabled,
            'short_commit': settings.ai_short_commit_enabled,
            'durable_runs': durable_runs_available,
            'crash_checkpoint': settings.ai_crash_checkpoint_enabled,
            'pen_extension': settings.ai_pen_extension_enabled,
            'music_extension': settings.ai_music_extension_enabled,
            'text_to_speech_extension': settings.ai_text_to_speech_extension_enabled,
        },
    }


if __name__ == '__main__':
    settings = get_settings()
    settings.require_api_key()
    import uvicorn
    uvicorn.run('app.main:app', host='127.0.0.1', port=8000, reload=False)
