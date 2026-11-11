import type { Api } from './api';
import { ShortcutEditor } from './shortcut-editor';
import type { AppSettings } from './types';
import { element, messageOf } from './utils';

export const defaultAppSettings: AppSettings = {
  theme: 'homebrew',
  fontSize: 13,
  cursorBlink: true,
  scrollback: 10_000,
  downloadDirectory: '',
  shortcuts: [],
};

export class SettingsManager {
  current: AppSettings = { ...defaultAppSettings };

  private readonly dialog = element<HTMLDialogElement>('settings-dialog');
  private readonly form = element<HTMLFormElement>('settings-form');
  private readonly shortcuts = new ShortcutEditor('settings-shortcut-list', 'settings-add-shortcut');

  constructor(
    private readonly api: Api,
    private readonly onChange: (settings: AppSettings) => void,
    private readonly onDownloadPath: (path: string) => void,
    private readonly onError: (message: string) => void,
  ) {
    this.form.addEventListener('submit', (event) => {
      event.preventDefault();
      void this.save();
    });
    element<HTMLButtonElement>('settings-choose-download').addEventListener('click', () => void this.chooseDownloadDirectory());
  }

  async load(): Promise<void> {
    this.current = await this.api.getSettings();
    this.apply();
  }

  open(): void {
    if (this.dialog.open) return;
    this.populate();
    this.dialog.showModal();
  }

  private populate(): void {
    const data = this.current;
    setValue(this.form, 'theme', data.theme);
    setValue(this.form, 'fontSize', String(data.fontSize));
    setValue(this.form, 'scrollback', String(data.scrollback));
    setValue(this.form, 'downloadDirectory', data.downloadDirectory);
    (this.form.elements.namedItem('cursorBlink') as HTMLInputElement).checked = data.cursorBlink;
    this.shortcuts.set(data.shortcuts);
  }

  private async save(): Promise<void> {
    const data = new FormData(this.form);
    const settings: AppSettings = {
      theme: data.get('theme') as AppSettings['theme'],
      fontSize: Number(data.get('fontSize')),
      cursorBlink: data.get('cursorBlink') === 'on',
      scrollback: Number(data.get('scrollback')),
      downloadDirectory: String(data.get('downloadDirectory') ?? ''),
      shortcuts: this.shortcuts.value(),
    };
    const submit = this.form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    submit.disabled = true;
    try {
      this.current = await this.api.updateSettings(settings);
      this.apply();
      this.dialog.close();
      this.onError('全局设置已保存；已有 Session 的快捷键保持不变');
    } catch (error) {
      this.onError(messageOf(error));
    } finally {
      submit.disabled = false;
    }
  }

  private async chooseDownloadDirectory(): Promise<void> {
    try {
      const path = await this.api.chooseDownloadDirectory();
      if (!path) return;
      this.current.downloadDirectory = path;
      setValue(this.form, 'downloadDirectory', path);
      this.onDownloadPath(path);
    } catch (error) {
      this.onError(messageOf(error));
    }
  }

  private apply(): void {
    this.onChange({ ...this.current, shortcuts: this.current.shortcuts.map((shortcut) => ({ ...shortcut })) });
    this.onDownloadPath(this.current.downloadDirectory);
  }
}

function setValue(form: HTMLFormElement, name: string, value: string): void {
  const input = form.elements.namedItem(name) as HTMLInputElement | HTMLSelectElement;
  input.value = value;
  if (input instanceof HTMLSelectElement) input.dispatchEvent(new Event('change'));
}
