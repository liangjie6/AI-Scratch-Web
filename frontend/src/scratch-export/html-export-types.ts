export type HtmlExportSourceMode = 'current' | 'file';

export interface HtmlExportOptions {
  sourceMode: HtmlExportSourceMode;
  title: string;
  filename: string;
  description: string;
  compatibleMode: boolean;
  turboMode: boolean;
  runDefault: boolean;
  width?: number;
  height?: number;
}

export type HtmlExportStatus = 'idle' | 'preparing' | 'packaging' | 'success' | 'error';

export interface HtmlExportResult {
  html: string;
  filename: string;
  size: number;
  blob: Blob;
  blobUrl?: string;
}
