import test from 'node:test';
import assert from 'node:assert/strict';
import { terminalThemes } from './terminal-themes.ts';

test('provides Homebrew as the black and green default palette', () => {
  assert.equal(terminalThemes.homebrew.background, '#000000');
  assert.equal(terminalThemes.homebrew.foreground, '#28FE14');
  assert.equal(terminalThemes.homebrew.cursor, '#38FE27');
  assert.equal(terminalThemes.homebrew.green, '#28FE14');
  assert.equal(terminalThemes.homebrew.brightGreen, '#28FE14');
});

test('provides the macOS Clear Dark ANSI palette', () => {
  assert.equal(terminalThemes.clearDark.background, '#191D27');
  assert.equal(terminalThemes.clearDark.foreground, '#E0E0E0');
  assert.equal(terminalThemes.clearDark.red, '#B45648');
  assert.equal(terminalThemes.clearDark.green, '#6CAA71');
  assert.equal(terminalThemes.clearDark.brightBlue, '#67B5ED');
  assert.equal(terminalThemes.clearDark.brightWhite, '#E5EFF5');
});

test('keeps the original terminal palette as Boring', () => {
  assert.equal(terminalThemes.boring.background, '#0B0E12');
  assert.equal(terminalThemes.boring.foreground, '#D7DCE2');
  assert.equal(terminalThemes.boring.cursor, '#4C8DFF');
  assert.equal(terminalThemes.boring.green, '#7EE787');
});
