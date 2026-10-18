import { createContext, useContext } from 'react';

export interface DraftNamespace {
  databaseId: string;
  clientId: string;
}

export const DraftNamespaceContext = createContext<DraftNamespace | null>(null);

export function useDraftNamespace() {
  const value = useContext(DraftNamespaceContext);
  if (!value) throw new Error('Text Editor needs a draft namespace.');
  return value;
}
