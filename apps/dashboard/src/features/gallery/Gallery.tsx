import { useState } from 'react';
import {
  Button,
  Checkbox,
  Field,
  IconButton,
  PixelIcon,
  Select,
  StatusBar,
  Tabs,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';

export function Gallery({ desktop }: AppWindowProps) {
  const [tab, setTab] = useState('controls');
  const [checked, setChecked] = useState(true);
  const [choice, setChoice] = useState('nextstep');
  const [status, setStatus] = useState('Ready');
  const [text, setText] = useState('Hello, AXV!');
  return (
    <div className="gallery-app">
      <div className="app-pad">
        <div className="panel-heading">
          <PixelIcon name="gallery" />
          <div>
            <strong>Component Gallery</strong>
            <p>Controls, panels, menus, and keyboard behavior.</p>
          </div>
        </div>
        <Tabs
          label="Component categories"
          value={tab}
          onChange={setTab}
          tabs={[
            { id: 'controls', label: 'Controls' },
            { id: 'dialogs', label: 'Dialogs & Menus' },
            { id: 'icons', label: 'Icons' },
          ]}
        >
          {tab === 'controls' && (
            <div className="gallery-controls">
              <Field
                label="Text field"
                value={text}
                onChange={(event) => setText(event.target.value)}
              />
              <Field label="Disabled field" value="Not available" disabled />
              <Select
                label="Pop-up menu"
                value={choice}
                onChange={(event) => setChoice(event.target.value)}
              >
                <option value="nextstep">AXV desktop</option>
                <option value="classic">Classic</option>
                <option value="custom">Custom</option>
              </Select>
              <Checkbox
                label="Enable this option"
                checked={checked}
                onChange={(event) => setChecked(event.target.checked)}
              />
              <Checkbox label="Unavailable option" disabled />
              <fieldset>
                <legend>Radio buttons</legend>
                <label>
                  <input type="radio" name="gallery-radio" defaultChecked />{' '}
                  First option
                </label>
                <label>
                  <input type="radio" name="gallery-radio" /> Second option
                </label>
              </fieldset>
              <div className="app-actions">
                <Button onClick={() => setStatus('Button pressed.')}>
                  Button
                </Button>
                <Button
                  primary
                  onClick={() => setStatus('Default action completed.')}
                >
                  Default
                </Button>
                <Button disabled>Disabled</Button>
              </div>
              <div
                className="app-actions"
                role="group"
                aria-label="Icon buttons"
              >
                <IconButton
                  icon="back"
                  label="Back"
                  onClick={() => setStatus('Back pressed.')}
                />
                <IconButton
                  icon="up"
                  label="Up"
                  onClick={() => setStatus('Up pressed.')}
                />
                <IconButton
                  icon="copy"
                  label="Copy"
                  onClick={() => setStatus('Copy pressed.')}
                />
                <IconButton
                  icon="paste"
                  label="Paste"
                  onClick={() => setStatus('Paste pressed.')}
                />
                <IconButton
                  icon="attachment"
                  label="Attachments"
                  onClick={() => setStatus('Attachments pressed.')}
                />
                <IconButton icon="page-up" label="Page Up" disabled />
                <IconButton icon="page-down" label="Page Down" disabled />
                <IconButton
                  icon="refresh"
                  label="Refresh"
                  onClick={() => setStatus('Refreshed.')}
                />
                <IconButton
                  icon="more"
                  label="More actions"
                  aria-haspopup="menu"
                  onClick={(event) => {
                    const rect = event.currentTarget.getBoundingClientRect();
                    desktop.contextMenu(
                      [
                        {
                          label: 'Details',
                          action: () => setStatus('Details selected.'),
                        },
                      ],
                      { x: rect.left, y: rect.bottom },
                      { title: 'Actions' },
                    );
                  }}
                />
              </div>
              <label>
                Progress
                <progress value={checked ? 65 : 30} max="100" />
              </label>
            </div>
          )}
          {tab === 'dialogs' && (
            <div className="gallery-controls">
              <p>
                Dialogs keep keyboard focus inside, support Return and Escape,
                and return focus to their origin.
              </p>
              <Button
                onClick={() =>
                  void desktop
                    .dialog({
                      title: 'An AXV Dialog',
                      message: 'Your changes have been saved.',
                      buttons: [{ label: 'OK', value: 'ok', default: true }],
                    })
                    .then(() => setStatus('Dialog closed.'))
                }
              >
                Show Alert
              </Button>
              <Button
                onClick={() =>
                  void desktop
                    .dialog({
                      title: 'Name Your Document',
                      input: { label: 'Name', value: 'Untitled.txt' },
                      buttons: [
                        { label: 'Cancel', value: 'cancel', cancel: true },
                        { label: 'Save', value: 'save', default: true },
                      ],
                    })
                    .then((answer) =>
                      setStatus(
                        answer?.button === 'save'
                          ? `Selected: ${answer.value}`
                          : 'Cancelled',
                      ),
                    )
                }
              >
                Show Input Dialog
              </Button>
              <Button
                onClick={(event) => {
                  const rect = event.currentTarget.getBoundingClientRect();
                  desktop.contextMenu(
                    [
                      {
                        label: 'First command',
                        action: () => setStatus('First command selected.'),
                      },
                      {
                        label: 'Selected option',
                        checked: true,
                        action: () => setStatus('Selected option.'),
                      },
                      { separator: true },
                      {
                        label: 'Submenu',
                        children: [
                          {
                            label: 'Nested command',
                            action: () => setStatus('Nested command selected.'),
                          },
                        ],
                      },
                      { label: 'Unavailable', disabled: true },
                    ],
                    { x: rect.left, y: rect.bottom },
                  );
                }}
              >
                Show Menu
              </Button>
            </div>
          )}
          {tab === 'icons' && (
            <div className="icon-gallery">
              {(
                [
                  'computer',
                  'folder',
                  'project',
                  'document',
                  'notes',
                  'trash',
                  'appearance',
                  'gallery',
                  'image',
                  'network',
                  'virtual-machines',
                  'virtual-machine',
                  'info',
                  'save',
                  'terminal',
                ] as const
              ).map((name) => (
                <div key={name}>
                  <PixelIcon name={name} />
                  <span>{name}</span>
                </div>
              ))}
            </div>
          )}
        </Tabs>
      </div>
      <StatusBar>{status}</StatusBar>
    </div>
  );
}
