import type {BitmapStore} from '../model/bitmap-store';
import type {CanvasDocument} from '../model/canvas-types';
import {canvasToBlob, rasterizeLayers} from '../pixels/rasterize';
import type {CostumeExport} from '../../scratch/asset-adapter';

const OUTPUT_WIDTH = 960;
const OUTPUT_HEIGHT = 720;

export async function exportCanvasTarget(documentState: CanvasDocument, bitmaps: BitmapStore, target: 'sprite' | 'backdrop'): Promise<CostumeExport> {
  const visibleLayers = documentState.layers.filter(layer => layer.visible && layer.opacity > 0);
  if (!visibleLayers.length) throw new Error('画布中没有可导出的可见图层');
  const board = documentState.artboard;
  if (!(board.width > 0 && board.height > 0)) throw new Error('画板尺寸无效');
  const sourceScale = Math.min(1, OUTPUT_WIDTH / board.width, OUTPUT_HEIGHT / board.height);
  const source = rasterizeLayers(documentState, bitmaps, board, visibleLayers, sourceScale);
  const canvas = document.createElement('canvas');
  canvas.width = OUTPUT_WIDTH;
  canvas.height = OUTPUT_HEIGHT;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('当前浏览器无法创建导出画布');
  if (target === 'backdrop') {
    context.fillStyle = '#fff';
    context.fillRect(0, 0, OUTPUT_WIDTH, OUTPUT_HEIGHT);
  }
  const scale = Math.min(OUTPUT_WIDTH / source.width, OUTPUT_HEIGHT / source.height);
  const width = source.width * scale;
  const height = source.height * scale;
  context.imageSmoothingEnabled = true;
  context.imageSmoothingQuality = 'high';
  context.drawImage(source, (OUTPUT_WIDTH - width) / 2, (OUTPUT_HEIGHT - height) / 2, width, height);
  return {
    name: documentState.name || (target === 'backdrop' ? '画布背景' : '画布角色'),
    blob: await canvasToBlob(canvas),
    dataFormat: 'png',
    assetWidth: OUTPUT_WIDTH,
    assetHeight: OUTPUT_HEIGHT,
    rotationCenterX: OUTPUT_WIDTH / 2,
    rotationCenterY: OUTPUT_HEIGHT / 2,
    bitmapResolution: 2
  };
}
