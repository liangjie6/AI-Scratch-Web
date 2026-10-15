import type {CostumeExport, SpriteExport} from './asset-adapter';

// Scratch paint draws bitmap costumes onto this fixed canvas. Changing only
// the sprite's stage size leaves the original pixels outside that canvas.
const BITMAP_EDITOR_WIDTH = 960;
const BITMAP_EDITOR_HEIGHT = 720;

function validateCostumeMetadata(costume: CostumeExport) {
  if (!Number.isFinite(costume.assetWidth) || !Number.isFinite(costume.assetHeight) || costume.assetWidth <= 0 || costume.assetHeight <= 0 || costume.bitmapResolution !== (costume.dataFormat === 'svg' ? 1 : 2)) throw new Error(costume.name + ' 的资源规格无效');
  if (!Number.isFinite(costume.rotationCenterX) || !Number.isFinite(costume.rotationCenterY)) throw new Error(costume.name + ' 的旋转中心无效');
}

export function bitmapCostumeEditorScale(costume: CostumeExport): number {
  validateCostumeMetadata(costume);
  if (costume.dataFormat !== 'png') return 1;
  const horizontalExtent = Math.max(Math.abs(costume.rotationCenterX), Math.abs(costume.assetWidth - costume.rotationCenterX));
  const verticalExtent = Math.max(Math.abs(costume.rotationCenterY), Math.abs(costume.assetHeight - costume.rotationCenterY));
  const scale = Math.min(1, BITMAP_EDITOR_WIDTH / 2 / horizontalExtent, BITMAP_EDITOR_HEIGHT / 2 / verticalExtent);
  // Leave one pixel for rounding at the bitmap boundary, including costumes
  // whose custom rotation center lies outside the image.
  return scale < 1 ? Math.min((BITMAP_EDITOR_WIDTH / 2 - 1) / horizontalExtent, (BITMAP_EDITOR_HEIGHT / 2 - 1) / verticalExtent) : scale;
}

export async function normalizeCostumeForEditor(costume: CostumeExport, scale = bitmapCostumeEditorScale(costume)): Promise<CostumeExport> {
  validateCostumeMetadata(costume);
  if (scale >= 1) return costume;
  if (!(scale > 0) || !Number.isFinite(scale)) throw new Error(costume.name + ' 的资源规格无效');

  if (costume.dataFormat === 'svg') {
    // Scale vectors together with the bitmap costumes in the same sprite so
    // switching between costumes keeps their relative size and shared pivot.
    const document = new DOMParser().parseFromString(await costume.blob.text(), 'image/svg+xml');
    const svg = document.documentElement;
    if (svg.localName !== 'svg' || document.querySelector('parsererror')) throw new Error(costume.name + ' 不是有效 SVG');
    if (!svg.hasAttribute('viewBox')) svg.setAttribute('viewBox', `0 0 ${costume.assetWidth} ${costume.assetHeight}`);
    svg.setAttribute('width', String(costume.assetWidth * scale));
    svg.setAttribute('height', String(costume.assetHeight * scale));
    return {...costume, blob: new Blob([new XMLSerializer().serializeToString(svg)], {type: 'image/svg+xml'}), assetWidth: costume.assetWidth * scale, assetHeight: costume.assetHeight * scale, rotationCenterX: costume.rotationCenterX * scale, rotationCenterY: costume.rotationCenterY * scale};
  }

  const bitmap = await createImageBitmap(costume.blob);
  try {
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.ceil(costume.assetWidth * scale));
    canvas.height = Math.max(1, Math.ceil(costume.assetHeight * scale));
    const context = canvas.getContext('2d');
    if (!context) throw new Error('当前浏览器不支持角色图片缩放');
    context.imageSmoothingQuality = 'high';
    context.drawImage(bitmap, 0, 0, costume.assetWidth * scale, costume.assetHeight * scale);
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('角色图片编码失败')), 'image/png'));
    return {...costume, blob, assetWidth: canvas.width, assetHeight: canvas.height, rotationCenterX: costume.rotationCenterX * scale, rotationCenterY: costume.rotationCenterY * scale};
  } finally {
    bitmap.close();
  }
}

export async function normalizeSpriteForEditor(sprite: SpriteExport): Promise<SpriteExport> {
  const scale = Math.min(1, ...sprite.costumes.map(bitmapCostumeEditorScale));
  if (scale >= 1) return sprite;
  const costumes = await Promise.all(sprite.costumes.map(costume => normalizeCostumeForEditor(costume, scale)));
  return {...sprite, costumes, initialSize: Math.min(100, sprite.initialSize / scale)};
}
