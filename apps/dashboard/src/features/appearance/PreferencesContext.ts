import { createContext, useContext } from 'react';
import type { Preferences } from './preferences';

export interface AppearanceData {
  preferences: Preferences;
  savePreferences: (patch: Partial<Preferences>) => Promise<void>;
}

export const PreferencesContext = createContext<AppearanceData | null>(null);

export function useAppearanceData() {
  const value = useContext(PreferencesContext);
  if (!value) throw new Error('Appearance needs PreferencesContext.');
  return value;
}
