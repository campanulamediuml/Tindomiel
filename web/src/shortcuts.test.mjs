import test from 'node:test';
import assert from 'node:assert/strict';
import { encodeShortcut, shortcutFromEvent } from './shortcuts.ts';

function event(key, modifiers = {}) {
  return { key, ctrlKey: false, altKey: false, shiftKey: false, metaKey: false, ...modifiers };
}

function bytes(shortcut) {
  return Array.from(encodeShortcut(shortcut));
}

test('captures canonical modifier combinations', () => {
  assert.equal(shortcutFromEvent(event('a', { ctrlKey: true })), 'Ctrl+A');
  assert.equal(shortcutFromEvent(event('k', { metaKey: true, shiftKey: true })), 'Shift+Cmd+K');
  assert.equal(shortcutFromEvent(event('+', { ctrlKey: true })), 'Ctrl+Plus');
  assert.equal(shortcutFromEvent(event('Control', { ctrlKey: true })), '');
});

test('encodes control and alt input as terminal bytes', () => {
  assert.deepEqual(bytes('Ctrl+A'), [1]);
  assert.deepEqual(bytes('Ctrl+Shift+A'), [1]);
  assert.deepEqual(bytes('Alt+B'), [27, 98]);
  assert.deepEqual(bytes('Alt+Shift+B'), [27, 66]);
  assert.deepEqual(bytes('Ctrl+Alt+A'), [27, 1]);
  assert.deepEqual(bytes('Ctrl+Space'), [0]);
});

test('encodes navigation and editing keys', () => {
  assert.deepEqual(bytes('Shift+Tab'), [27, 91, 90]);
  assert.deepEqual(bytes('ArrowUp'), [27, 91, 65]);
  assert.deepEqual(bytes('Ctrl+ArrowRight'), [27, 91, 49, 59, 53, 67]);
  assert.deepEqual(bytes('Delete'), [27, 91, 51, 126]);
  assert.deepEqual(bytes('Ctrl+Delete'), [27, 91, 51, 59, 53, 126]);
  assert.deepEqual(bytes('Enter'), [13]);
  assert.deepEqual(bytes('Backspace'), [127]);
});

test('encodes function keys', () => {
  assert.deepEqual(bytes('F1'), [27, 79, 80]);
  assert.deepEqual(bytes('Shift+F1'), [27, 91, 49, 59, 50, 80]);
  assert.deepEqual(bytes('F12'), [27, 91, 50, 52, 126]);
});

test('rejects incomplete or unsupported combinations', () => {
  assert.deepEqual(bytes(''), []);
  assert.deepEqual(bytes('NotAKey'), []);
  assert.deepEqual(bytes('Ctrl+NotAKey'), []);
});
