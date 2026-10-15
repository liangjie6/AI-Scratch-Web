package prompt

import "fmt"

// TextExpansionPrompts holds the system prompt for each expansion kind.
var TextExpansionPrompts = map[string]string{
	"scene": "你是专业的图像生成提示词编辑器。将用户的简短描述扩写为一段可直接用于文生图模型的中文画面描述。" +
		"补充主体、环境、构图、镜头、光线、色彩、氛围、材质和关键细节；保持原意，不添加相互矛盾的内容。" +
		"输出规则具有最高优先级：只输出扩写后的画面描述正文，控制在 700 个中文字符以内；完成正文后立即停止。" +
		"禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；" +
		"禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown、参数或负面提示词。",
	"style": "你是专业的视觉风格设计师。将用户给出的风格词扩写为一段可直接用于文生图模型的中文风格说明。" +
		"补充媒介、造型语言、线条、色彩、光影、质感、渲染以及时代或流派特征；保持原意，不描述具体画面主体。" +
		"输出规则具有最高优先级：只输出扩写后的风格说明正文，控制在 200 个中文字符以内；完成正文后立即停止。" +
		"禁止自我介绍、问候、解释、追问、建议、免责声明、分隔线或任何正文之外的内容；" +
		"禁止提及 Agnes、Sapiens AI、模型、助手或用户。不要加入标题、引号、Markdown或参数。",
}

// BuildTextExpansionMessages mirrors build_text_expansion_messages.
func BuildTextExpansionMessages(kind, text string) []map[string]string {
	return []map[string]string{
		{"role": "system", "content": TextExpansionPrompts[kind]},
		{
			"role":    "user",
			"content": "严格遵守输出规则，只返回扩写后的正文，不要附带任何其他内容。\n待扩写内容：\n" + text,
		},
	}
}

// StyleLabels maps a built-in style to its provider description.
var StyleLabels = map[string]string{
	"cartoon":    "bright child-friendly cartoon illustration",
	"scratch":    "Scratch-inspired 2D vector art with bold clean outlines, simple readable shapes, bright flat colors, minimal shading, and a playful child-friendly game-project look",
	"paper-cut":  "layered paper-cut illustration with crisp silhouettes",
	"pixel":      "clean pixel-art game scene with readable shapes",
	"watercolor": "soft watercolor illustration with clear focal objects",
}

// BuildImagePrompt separates scene composition from extractable sprite assets.
func BuildImagePrompt(style, customStyle, userPrompt string, width, height int, target, background string) (string, error) {
	if style == "custom" && customStyle == "" {
		return "", fmt.Errorf("custom_style is required when style is custom")
	}
	styleDescription, ok := StyleLabels[style]
	if style == "custom" {
		styleDescription = customStyle
		ok = true
	}
	if !ok {
		return "", fmt.Errorf("unknown style")
	}
	if target == "backdrop" {
		return fmt.Sprintf(
			"Create one complete image for a Scratch project. "+
				"User scene: %s. Style: %s. "+
				"Canvas intent is %d by %d. "+
				"For a backdrop, fill the scene edge to edge with a clear foreground and background. "+
				"Use one coherent composition with no borders, grid lines, labels, text, watermark, or collage.",
			userPrompt, styleDescription, width, height,
		), nil
	}

	var bgHint string
	if background == "transparent" {
		bgHint = "Use true alpha transparency with no green, white, or checkerboard background. Leave all space around and between subjects fully transparent, with no scenery, floor, cast shadows, or halos."
	} else {
		bgHint = "Use a uniform saturated #00ff00 green background across the entire canvas including holes. Every background pixel must be exactly RGB(0, 255, 0), like a flat digital color fill: no texture, noise, grain, pattern, lighting, gradients, shadows, floor, or scenery. Apply the requested art style only to the subjects, never to the background, and avoid green subject colors."
	}

	spriteHint := "For a sprite, isolate every requested character, pose, or object with whole visible silhouettes, generous gaps and outer padding for manual extraction, non-overlapping and non-touching subjects, and consistent scale. " +
		"Show each subject completely from head to feet, including all accessories, without cropping. Omit any scene or background described in the user prompt; only render the requested subjects. " +
		"When multiple subjects are requested, arrange them in regular rows and columns while retaining the exact count requested; render one subject if singular. " +
		"Do not force a fixed sheet grid or count. " + bgHint

	return fmt.Sprintf(
		"Create one complete image for a Scratch project. "+
			"User scene: %s. Style: %s. "+
			"Canvas intent is %d by %d. %s "+
			"No borders, grid lines, labels, text, or watermark.",
		userPrompt, styleDescription, width, height, spriteHint,
	), nil
}
