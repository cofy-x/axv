import { createContext, useContext } from 'react';
import type { WindowCommands } from './window-commands';

export const WindowCommandsContext = createContext<WindowCommands | null>(null);

export function useWindowCommands() {
  const value = useContext(WindowCommandsContext);
  if (!value) throw new Error('Window commands need WindowCommandsContext.');
  return value;
}
