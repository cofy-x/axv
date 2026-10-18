export interface Entry {
  id: string;
  parentId: string;
  name: string;
  kind: 'file' | 'folder';
  mime: string;
  size: number;
  revision: number;
  createdAt: string;
  updatedAt: string;
  trashedAt?: string;
  editable: boolean;
  previewable: boolean;
  system: boolean;
}
