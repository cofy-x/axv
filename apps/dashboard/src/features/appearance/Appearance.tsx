import { useState } from 'react';
import { Button, Checkbox, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { runCommand } from '../../shared/desktop/dialogs';
import { useAppearanceData } from './PreferencesContext';

export function Appearance({ desktop }: AppWindowProps) {
  const data = useAppearanceData();
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState('Saves automatically.');

  function update(patch: Partial<typeof data.preferences>) {
    setSaving(true);
    setStatus('Saving…');
    void runCommand(desktop, async () => {
      try {
        await data.savePreferences(patch);
        setStatus('Saved.');
      } catch (error) {
        setStatus('Not saved.');
        throw error;
      } finally {
        setSaving(false);
      }
    });
  }

  return (
    <div className="settings-app">
      <div className="app-pad">
        <div className="panel-heading">
          <PixelIcon name="appearance" />
          <div>
            <strong>Preferences</strong>
          </div>
        </div>
        <fieldset disabled={saving}>
          <legend>Desktop Pattern</legend>
          <div className="wallpaper-options">
            {(['slate', 'dither', 'solid'] as const).map((wallpaper) => (
              <button
                key={wallpaper}
                aria-pressed={data.preferences.wallpaper === wallpaper}
                className={`wallpaper-swatch ${wallpaper} ${data.preferences.wallpaper === wallpaper ? 'chosen' : ''}`}
                onClick={() => update({ wallpaper })}
              >
                <span className="swatch-preview" />
                <span>
                  {wallpaper === 'slate'
                    ? 'Slate'
                    : wallpaper === 'dither'
                      ? 'Dither'
                      : 'Solid Gray'}
                </span>
              </button>
            ))}
          </div>
        </fieldset>
        <fieldset disabled={saving}>
          <legend>Desktop Options</legend>
          <Checkbox
            label="Snap icons to grid"
            checked={data.preferences.iconSnap}
            onChange={(event) => update({ iconSnap: event.target.checked })}
          />
          <Checkbox
            label="24-hour clock"
            checked={data.preferences.clock24Hour}
            onChange={(event) => update({ clock24Hour: event.target.checked })}
          />
          <div className="app-actions">
            <Button onClick={() => desktop.arrangeIcons()}>
              Arrange Icons
            </Button>
          </div>
        </fieldset>
        <fieldset className="preferences-access-group">
          <legend>Remote access</legend>
          <div className="preferences-access-row">
            <span>
              <strong>Tailscale</strong>
              <span className="fine-print">Private network</span>
            </span>
            <Button
              aria-label="Open Tailscale"
              onClick={() => desktop.openApp('remote-access')}
            >
              Open
            </Button>
          </div>
          <div className="preferences-access-row">
            <span>
              <strong>Cloudflare</strong>
              <span className="fine-print">Browser access</span>
            </span>
            <Button
              aria-label="Open Cloudflare"
              onClick={() => desktop.openApp('cloudflare')}
            >
              Open
            </Button>
          </div>
        </fieldset>
      </div>
      <StatusBar>{status}</StatusBar>
    </div>
  );
}
