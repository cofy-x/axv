import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, Checkbox, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { runCommand } from '../../shared/desktop/dialogs';
import { consumeRemoteAccessRequest } from './navigation';
import { useAppearanceData } from './PreferencesContext';

export function Appearance({ desktop, interactive, window }: AppWindowProps) {
  const data = useAppearanceData();
  const [status, setStatus] = useState('');
  const saving = status === 'Saving…';
  const sectionId = useId();
  const remoteAccess = useRef<HTMLFieldSetElement>(null);
  const { openApp } = desktop;

  // Reopening Preferences also updates the window object for an existing ID.
  useLayoutEffect(() => {
    const section = remoteAccess.current;
    if (
      !interactive ||
      !section ||
      !consumeRemoteAccessRequest(openApp, window.id)
    ) {
      return;
    }
    section.scrollIntoView({ block: 'start' });
    section.focus({ preventScroll: true });
  }, [interactive, openApp, window]);

  useEffect(() => {
    if (status !== 'Saved.') return;
    const timeout = setTimeout(() => setStatus(''), 3000);
    return () => clearTimeout(timeout);
  }, [status]);

  function update(patch: Partial<typeof data.preferences>) {
    setStatus('Saving…');
    void runCommand(desktop, async () => {
      try {
        await data.savePreferences(patch);
        setStatus('Saved.');
      } catch (error) {
        setStatus('Not saved.');
        throw error;
      }
    });
  }

  return (
    <div className="settings-app">
      <div className="app-pad">
        <fieldset
          className="preferences-section"
          aria-labelledby={`${sectionId}-wallpaper`}
          disabled={saving}
        >
          <h2 id={`${sectionId}-wallpaper`}>Wallpaper</h2>
          <div className="wallpaper-options">
            {(['slate', 'dither', 'solid'] as const).map((wallpaper) => (
              <button
                type="button"
                key={wallpaper}
                aria-pressed={data.preferences.wallpaper === wallpaper}
                className="wallpaper-swatch"
                onClick={() => update({ wallpaper })}
              >
                <span
                  className={`swatch-preview ns-wallpaper-${wallpaper}`}
                  aria-hidden="true"
                />
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
        <fieldset
          className="preferences-section"
          aria-labelledby={`${sectionId}-desktop`}
          disabled={saving}
        >
          <h2 id={`${sectionId}-desktop`}>Desktop</h2>
          <div className="preferences-row">
            <Checkbox
              label="Snap icons to grid"
              checked={data.preferences.iconSnap}
              onChange={(event) => update({ iconSnap: event.target.checked })}
            />
            <Button onClick={() => desktop.arrangeIcons()}>
              Arrange Icons
            </Button>
          </div>
          <Checkbox
            label="24-hour clock"
            checked={data.preferences.clock24Hour}
            onChange={(event) => update({ clock24Hour: event.target.checked })}
          />
        </fieldset>
        <fieldset
          className="preferences-section"
          aria-labelledby={`${sectionId}-remote-access`}
          ref={remoteAccess}
          tabIndex={-1}
        >
          <h2 id={`${sectionId}-remote-access`}>Remote access</h2>
          <div className="preferences-row">
            <span>Private network</span>
            <Button onClick={() => desktop.openApp('remote-access')}>
              Open Tailscale
            </Button>
          </div>
          <div className="preferences-row">
            <span>Browser access</span>
            <Button onClick={() => desktop.openApp('cloudflare')}>
              Open Cloudflare
            </Button>
          </div>
        </fieldset>
        <fieldset
          className="preferences-section"
          aria-labelledby={`${sectionId}-notifications`}
        >
          <div className="preferences-row">
            <h2 id={`${sectionId}-notifications`}>Notifications</h2>
            <Button
              aria-label="Open Notifications"
              onClick={() => desktop.openApp('notifications')}
            >
              Open
            </Button>
          </div>
        </fieldset>
      </div>
      <StatusBar className="preferences-status">{status}</StatusBar>
    </div>
  );
}
