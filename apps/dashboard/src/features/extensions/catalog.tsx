import { createContext, useContext } from 'react';
import type { BuiltinApplication } from './types';

export const ApplicationCatalogContext = createContext<{
  builtins: BuiltinApplication[];
  clientId: string;
}>({ builtins: [], clientId: '' });

export const useApplicationCatalog = () =>
  useContext(ApplicationCatalogContext);
