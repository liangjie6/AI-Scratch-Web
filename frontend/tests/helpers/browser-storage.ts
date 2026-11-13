import 'fake-indexeddb/auto';
import {beforeEach, vi} from 'vitest';

// Keep native Node Blob/structuredClone so IndexedDB really persists and reads
// the bytes. A jsdom Blob cannot be structured-cloned by fake-indexeddb.
class TestSessionStorage {
  private readonly values = new Map<string, string>();
  get length() {return this.values.size;}
  getItem(key: string) {return this.values.get(key) ?? null;}
  setItem(key: string, value: string) {this.values.set(key, String(value));}
  removeItem(key: string) {this.values.delete(key);}
  clear() {this.values.clear();}
  key(index: number) {return [...this.values.keys()][index] ?? null;}
}

beforeEach(() => {
  vi.stubGlobal('sessionStorage', new TestSessionStorage());
});
