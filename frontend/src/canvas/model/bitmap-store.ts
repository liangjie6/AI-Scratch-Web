import {createId} from './create-id';

export interface BitmapSnapshot { id: string; width: number; height: number; data: Uint8ClampedArray; revision: number; vectorSource?: string; vectorDirty?: boolean }
export class BitmapStore {
  private resources = new Map<string, BitmapSnapshot>();
  put(image: ImageData, id: string = createId(), vectorSource?: string) { this.resources.set(id, {id, width: image.width, height: image.height, data: new Uint8ClampedArray(image.data), revision: 1, ...(vectorSource ? {vectorSource} : {})}); return id; }
  get(id: string) { const item = this.resources.get(id); if (!item) throw new Error('图层位图已丢失'); return item; }
  getImageData(id: string) { const item = this.get(id); return new ImageData(new Uint8ClampedArray(item.data), item.width, item.height); }
  update(id: string, image: ImageData) { const current = this.get(id); current.width = image.width; current.height = image.height; current.data = new Uint8ClampedArray(image.data); current.revision += 1; if(current.vectorSource)current.vectorDirty=true; }
  clone(id: string) { const source = this.get(id), cloneId = createId(); this.resources.set(cloneId, {...source, id: cloneId, data: new Uint8ClampedArray(source.data), revision: 1}); return cloneId; }
  remove(id: string) { this.resources.delete(id); }
  setSnapshot(item: BitmapSnapshot) { this.resources.set(item.id, {...item, data: new Uint8ClampedArray(item.data)}); }
  entries() { return [...this.resources.values()]; }
  snapshot() { return this.entries().map(item => ({...item, data: new Uint8ClampedArray(item.data)})); }
  restore(snapshot: BitmapSnapshot[]) { this.resources = new Map(snapshot.map(item => [item.id, {...item, data: new Uint8ClampedArray(item.data)}])); }
}
