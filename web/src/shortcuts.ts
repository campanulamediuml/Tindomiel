const textEncoder = new TextEncoder();

interface ShortcutEventLike {
  key: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}

interface ShortcutParts {
  normalizedKey: string;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  metaKey: boolean;
}

export function shortcutFromEvent(event: ShortcutEventLike): string {
  if (['Control', 'Alt', 'Shift', 'Meta'].includes(event.key)) return '';
  const parts: string[] = [];
  if (event.ctrlKey) parts.push('Ctrl');
  if (event.altKey) parts.push('Alt');
  if (event.shiftKey) parts.push('Shift');
  if (event.metaKey) parts.push('Cmd');
  parts.push(normalizeKey(event.key));
  return parts.join('+');
}

export function encodeShortcut(shortcut: string): Uint8Array {
  const parts = parseShortcut(shortcut);
  if (!parts) return new Uint8Array();
  const { normalizedKey: key, ctrlKey, altKey, shiftKey, metaKey } = parts;
  const escapePrefix = altKey || metaKey ? '\x1b' : '';

  if (ctrlKey) {
    const control = controlByte(key);
    if (control !== undefined) return bytes(escapePrefix + String.fromCharCode(control));
  }
  if (key === 'Tab' && shiftKey) return bytes('\x1b[Z');

  const simple: Record<string, string> = {
    Space: ' ',
    Enter: '\r',
    Tab: '\t',
    Escape: '\x1b',
    Backspace: '\x7f',
  };
  if (simple[key] !== undefined) return bytes(escapePrefix + simple[key]);

  const modifier = 1 + (shiftKey ? 1 : 0) + (altKey ? 2 : 0) + (ctrlKey ? 4 : 0) + (metaKey ? 8 : 0);
  const suffix = modifier > 1 ? `;${modifier}` : '';
  const arrows: Record<string, string> = { ArrowUp: 'A', ArrowDown: 'B', ArrowRight: 'C', ArrowLeft: 'D', Home: 'H', End: 'F' };
  if (arrows[key]) return bytes(`\x1b[${modifier > 1 ? `1${suffix}` : ''}${arrows[key]}`);
  const numbered: Record<string, number> = { Insert: 2, Delete: 3, PageUp: 5, PageDown: 6 };
  if (numbered[key]) return bytes(`\x1b[${numbered[key]}${suffix}~`);
  const functionKeys: Record<string, string> = { F1: 'P', F2: 'Q', F3: 'R', F4: 'S' };
  if (functionKeys[key]) return bytes(modifier > 1 ? `\x1b[1${suffix}${functionKeys[key]}` : `\x1bO${functionKeys[key]}`);
  const functionNumbers: Record<string, number> = { F5: 15, F6: 17, F7: 18, F8: 19, F9: 20, F10: 21, F11: 23, F12: 24 };
  if (functionNumbers[key]) return bytes(`\x1b[${functionNumbers[key]}${suffix}~`);

  const printable = key === 'Plus' ? '+' : key === 'Minus' ? '-' : key;
  if (printable.length === 0 || Array.from(printable).length !== 1) return new Uint8Array();
  const output = !shiftKey && !ctrlKey ? printable.toLowerCase() : printable;
  return bytes(escapePrefix + output);
}

function parseShortcut(shortcut: string): ShortcutParts | undefined {
  const parts = shortcut.split('+');
  const normalizedKey = parts.pop();
  if (!normalizedKey) return undefined;
  const modifiers = new Set(parts);
  if (modifiers.size !== parts.length || Array.from(modifiers).some((part) => !['Ctrl', 'Alt', 'Shift', 'Cmd'].includes(part))) return undefined;
  return {
    normalizedKey,
    ctrlKey: modifiers.has('Ctrl'),
    altKey: modifiers.has('Alt'),
    shiftKey: modifiers.has('Shift'),
    metaKey: modifiers.has('Cmd'),
  };
}

function normalizeKey(key: string): string {
  if (key === ' ') return 'Space';
  if (key === '+') return 'Plus';
  if (key === '-') return 'Minus';
  if (key === 'Esc') return 'Escape';
  return key.length === 1 ? key.toUpperCase() : key;
}

function controlByte(key: string): number | undefined {
  if (key === 'Space') return 0;
  if (key === '?') return 127;
  if (key.length !== 1) return undefined;
  const code = key.toUpperCase().charCodeAt(0);
  return code >= 64 && code <= 95 ? code & 31 : undefined;
}

function bytes(value: string): Uint8Array {
  return textEncoder.encode(value);
}
