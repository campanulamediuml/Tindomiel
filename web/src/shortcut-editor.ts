import { shortcutFromEvent } from './shortcuts';
import type { ShortcutMapping } from './types';
import { element } from './utils';

export class ShortcutEditor {
  private shortcuts: ShortcutMapping[] = [];
  private readonly list: HTMLDivElement;

  constructor(listId: string, addButtonId: string) {
    this.list = element<HTMLDivElement>(listId);
    element<HTMLButtonElement>(addButtonId).addEventListener('click', () => {
      this.shortcuts.push({ from: '', to: '' });
      this.render();
      this.list.lastElementChild?.querySelector<HTMLInputElement>('input')?.focus();
    });
  }

  set(shortcuts: ShortcutMapping[]): void {
    this.shortcuts = shortcuts.map((shortcut) => ({ ...shortcut }));
    this.render();
  }

  value(): ShortcutMapping[] {
    return this.shortcuts.filter((shortcut) => shortcut.from && shortcut.to).map((shortcut) => ({ ...shortcut }));
  }

  private render(): void {
    this.list.replaceChildren();
    this.shortcuts.forEach((shortcut, index) => {
      const row = document.createElement('div');
      row.className = 'shortcut-row';
      for (const field of ['from', 'to'] as const) {
        const input = document.createElement('input');
        input.className = 'shortcut-input';
        input.readOnly = true;
        input.placeholder = '按下快捷键';
        input.value = shortcut[field];
        input.setAttribute('aria-label', field === 'from' ? '组合 A' : '映射为 B');
        input.addEventListener('keydown', (event) => {
          event.preventDefault();
          event.stopPropagation();
          const combination = shortcutFromEvent(event);
          if (!combination) return;
          shortcut[field] = combination;
          input.value = combination;
        });
        row.append(input);
      }
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'link-button danger';
      remove.textContent = '删除';
      remove.addEventListener('click', () => {
        this.shortcuts.splice(index, 1);
        this.render();
      });
      row.append(remove);
      this.list.append(row);
    });
  }
}
