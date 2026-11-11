import { ShortcutEditor } from './shortcut-editor';
import type { TerminalView } from './terminal';
import type { ShortcutMapping } from './types';
import { element } from './utils';

export class SessionSettingsManager {
  private terminal?: TerminalView;
  private readonly dialog = element<HTMLDialogElement>('session-settings-dialog');
  private readonly shortcuts = new ShortcutEditor('session-shortcut-list', 'session-add-shortcut');

  constructor(
    private readonly getGlobalShortcuts: () => ShortcutMapping[],
    private readonly onError: (message: string) => void,
  ) {
    element<HTMLFormElement>('session-settings-form').addEventListener('submit', (event) => {
      event.preventDefault();
      if (!this.terminal) return;
      this.terminal.setShortcuts(this.shortcuts.value());
      this.dialog.close();
      this.onError('Session 快捷键已保存，不影响其他 Session 或全局配置');
    });
    element<HTMLButtonElement>('session-sync-shortcuts').addEventListener('click', () => {
      this.shortcuts.set(this.getGlobalShortcuts());
      this.onError('已载入当前全局映射，点击保存后同步到此 Session');
    });
  }

  open(name: string, terminal: TerminalView): void {
    this.terminal = terminal;
    element<HTMLHeadingElement>('session-settings-title').textContent = `会话设置 · ${name}`;
    this.shortcuts.set(terminal.getShortcuts());
    this.dialog.showModal();
  }
}
