export type ImageTarget = 'backdrop' | 'sprite';
export type ImageStyle = 'cartoon' | 'scratch' | 'paper-cut' | 'pixel' | 'watercolor' | 'custom';
export type ImageModel = 'agnes-image-2.5-flash' | 'gpt-image-2.5-sunburst';

export const DEFAULT_IMAGE_MODEL: ImageModel = 'agnes-image-2.5-flash';

export const IMAGE_MODELS: Array<{value: ImageModel; label: string}> = [
  {value: 'agnes-image-2.5-flash', label: 'agnes-image-2.5-flash'},
  {value: 'gpt-image-2.5-sunburst', label: 'gpt-image-2.5-sunburst'}
];

export interface CanvasSize {
  width: number;
  height: number;
  label: string;
}

export interface ImageGenerationRequest {
  prompt: string;
  style: ImageStyle;
  custom_style?: string;
  width: number;
  height: number;
  target: ImageTarget;
  sheet_layout: '1x1';
  model?: ImageModel;
}

export interface SheetLayout {
  columns: 1;
  rows: 1;
}

export interface ImageProcessingHints {
  background_mode?: 'solid' | 'transparent';
  suggested_key_color?: string;
  sheet_layout?: {columns: number; rows: number};
}

export interface ImageGenerationResponse {
  request_id: string;
  mime_type: 'image/png';
  sheet_width: number;
  sheet_height: number;
  sheet_layout: SheetLayout;
  image_base64: string;
  target: ImageTarget;
  requested_canvas: {width: number; height: number};
  processing_hints?: ImageProcessingHints;
}

export const CANVAS_SIZES: CanvasSize[] = [
  {width: 480, height: 360, label: 'Scratch 舞台 · 480 × 360'},
  {width: 640, height: 360, label: '宽屏 · 640 × 360'},
  {width: 360, height: 360, label: '方形 · 360 × 360'},
  {width: 360, height: 480, label: '竖屏 · 360 × 480'}
];

export const IMAGE_STYLES: Array<{value: ImageStyle; label: string}> = [
  {value: 'cartoon', label: '明亮卡通'},
  {value: 'scratch', label: 'Scratch 风格'},
  {value: 'paper-cut', label: '纸艺剪影'},
  {value: 'pixel', label: '像素游戏'},
  {value: 'watercolor', label: '柔和水彩'},
  {value: 'custom', label: '自定义'}
];

