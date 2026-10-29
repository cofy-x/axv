import { describe, expect, it } from 'vitest';
import { Terminal } from '@xterm/xterm';

describe('xterm alternate-screen resizing', () => {
  it.each(['initial', 'reopened'])(
    'keeps the last row after shrinking an empty %s alternate screen',
    async (state) => {
      const terminal = new Terminal({ cols: 20, rows: 4, scrollback: 0 });
      const write = (data: string) =>
        new Promise<void>((resolve) => terminal.write(data, resolve));
      try {
        await write('Normal screen');
        if (state === 'reopened')
          await write('\x1b[?1049hPrevious alternate screen\x1b[?1049l');

        terminal.resize(20, 3);
        // An in-flight host repaint can still contain the previous four rows.
        // Its final cursor position need not be on the bottom row.
        await write(
          '\x1b[?1049h\x1b[Hfirst\r\nsecond\r\nthird\r\nfourth\x1b[H',
        );
        terminal.resize(20, 4);
        await write('\x1b[H\x1b[2Jone\r\ntwo\r\nthree\r\nready> ');

        const buffer = terminal.buffer.active;
        expect(buffer.type).toBe('alternate');
        expect(buffer.baseY).toBe(0);
        expect(buffer.viewportY).toBe(0);
        expect(buffer.length).toBe(terminal.rows);
        expect(
          Array.from({ length: terminal.rows }, (_, row) =>
            buffer.getLine(row)?.translateToString(true),
          ),
        ).toEqual(['one', 'two', 'three', 'ready> ']);

        await write('\x1b[?1049l');
        expect(terminal.buffer.active.type).toBe('normal');
        expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe(
          'Normal screen',
        );
      } finally {
        terminal.dispose();
      }
    },
  );
});
