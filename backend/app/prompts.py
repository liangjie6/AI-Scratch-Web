from .schemas.image_generation import ImageGenerationRequest
from .schemas.text_expansion import TextExpansionRequest


TEXT_EXPANSION_PROMPTS = {
    'scene': (
        '你是专业的图像生成提示词编辑器。将用户的简短描述扩写为一段可直接用于文生图模型的中文画面描述。'
        '补充主体、环境、构图、镜头、光线、色彩、氛围、材质和关键细节；保持原意，不添加相互矛盾的内容。'
        '输出规则具有最高优先级：只输出扩写后的画面描述正文，控制在 700 个中文字符以内；完成正文后立即停止。'
        '禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；'
        '禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown、参数或负面提示词。'
    ),
    'style': (
        '你是专业的视觉风格设计师。将用户给出的风格词扩写为一段可直接用于文生图模型的中文风格说明。'
        '补充媒介、造型语言、线条、色彩、光影、质感、渲染以及时代或流派特征；保持原意，不描述具体画面主体。'
        '输出规则具有最高优先级：只输出扩写后的风格说明正文，控制在 200 个中文字符以内；完成正文后立即停止。'
        '禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；'
        '禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown或参数。'
    ),
}


def build_text_expansion_messages(request: TextExpansionRequest) -> list[dict[str, str]]:
    return [
        {'role': 'system', 'content': TEXT_EXPANSION_PROMPTS[request.kind]},
        {
            'role': 'user',
            'content': f'严格遵守输出规则，只返回扩写后的正文，不要附带任何其他内容。\n待扩写内容：\n{request.text}',
        },
    ]


STYLE_LABELS = {
    'cartoon': 'bright child-friendly cartoon illustration',
    'scratch': 'Scratch-inspired 2D vector art with bold clean outlines, simple readable shapes, bright flat colors, minimal shading, and a playful child-friendly game-project look',
    'paper-cut': 'layered paper-cut illustration with crisp silhouettes',
    'pixel': 'clean pixel-art game scene with readable shapes',
    'watercolor': 'soft watercolor illustration with clear focal objects',
}


def build_image_prompt(request: ImageGenerationRequest) -> str:
    request.validate_canvas_size()
    if request.style == 'custom' and not request.custom_style:
        raise ValueError('custom_style is required when style is custom')
    style_description = request.custom_style if request.style == 'custom' else STYLE_LABELS[request.style]
    target_hint = (
        'For a backdrop, fill the scene edge to edge with a clear foreground and background.'
        if request.target == 'backdrop'
        else 'For a sprite, keep the subject centered and use a clean simple background; do not fake transparency.'
    )
    return (
        f'Create one complete image for a Scratch project. '
        f'User scene: {request.prompt}. Style: {style_description}. '
        f'Canvas intent is {request.width} by {request.height}. {target_hint} '
        'Use one coherent composition with no borders, grid lines, labels, text, watermark, or collage.'
    )

