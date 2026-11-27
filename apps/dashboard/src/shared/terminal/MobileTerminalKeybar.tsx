export interface MobileTerminalKeybarProps {
  disabled: boolean;
  ctrlActive: boolean;
  onToggleCtrl: () => void;
  onSend: (sequence: string) => void;
}

/**
 * Converts a single printable ASCII character into its Ctrl+key control byte
 * when sticky Ctrl is armed on a mobile/touch keyboard.
 */
export function applyCtrlModifier(data: string): string | null {
  if (data.length !== 1) return null;
  const code = data.charCodeAt(0);
  if (code >= 0x61 && code <= 0x7a) {
    // a-z -> 0x01-0x1a
    return String.fromCharCode(code - 0x60);
  }
  if (code >= 0x40 && code <= 0x5f) {
    // @, A-Z, [, \, ], ^, _ -> 0x00-0x1f
    return String.fromCharCode(code - 0x40);
  }
  return null;
}

const KEYS: readonly { label: string; ariaLabel: string; sequence: string }[] =
  [
    { label: 'Esc', ariaLabel: 'Escape', sequence: '\x1b' },
    { label: 'Tab', ariaLabel: 'Tab', sequence: '\t' },
    { label: '^C', ariaLabel: 'Ctrl+C', sequence: '\x03' },
    { label: '^D', ariaLabel: 'Ctrl+D', sequence: '\x04' },
    { label: '↑', ariaLabel: 'Arrow Up', sequence: '\x1b[A' },
    { label: '↓', ariaLabel: 'Arrow Down', sequence: '\x1b[B' },
    { label: '←', ariaLabel: 'Arrow Left', sequence: '\x1b[D' },
    { label: '→', ariaLabel: 'Arrow Right', sequence: '\x1b[C' },
    { label: '/', ariaLabel: 'Slash', sequence: '/' },
    { label: '-', ariaLabel: 'Dash', sequence: '-' },
    { label: '↵', ariaLabel: 'Enter', sequence: '\r' },
  ];

export function MobileTerminalKeybar({
  disabled,
  ctrlActive,
  onToggleCtrl,
  onSend,
}: MobileTerminalKeybarProps) {
  const keepFocus = (event: React.PointerEvent<HTMLButtonElement>) => {
    // Keep xterm's hidden textarea focused so soft keyboards stay open on phones.
    event.preventDefault();
  };

  return (
    <div
      className="terminal-touch-keybar"
      role="toolbar"
      aria-label="Terminal touch keys"
      data-testid="terminal-touch-keybar"
    >
      <button
        type="button"
        className={`terminal-touch-key${ctrlActive ? ' is-active' : ''}`}
        aria-label="Ctrl modifier"
        aria-pressed={ctrlActive}
        disabled={disabled}
        onPointerDown={keepFocus}
        onClick={onToggleCtrl}
      >
        Ctrl
      </button>
      {KEYS.map((key) => (
        <button
          key={key.ariaLabel}
          type="button"
          className="terminal-touch-key"
          aria-label={key.ariaLabel}
          disabled={disabled}
          onPointerDown={keepFocus}
          onClick={() => onSend(key.sequence)}
        >
          {key.label}
        </button>
      ))}
    </div>
  );
}
