import type { Api } from './api';
import type { AuthMethod, Folder, KeyInfo, Profile } from './types';
import { element, messageOf } from './utils';

export class ConnectionManager {
  private profiles: Profile[] = [];
  private folders: Folder[] = [];
  private keys: KeyInfo[] = [];
  private editingId?: string;
  private contextProfile?: Profile;
  private contextFolder = '';
  private readonly dialog = element<HTMLDialogElement>('connection-dialog');
  private readonly editor = element<HTMLDialogElement>('profile-editor-dialog');
  private readonly form = element<HTMLFormElement>('profile-form');

  constructor(
    private readonly api: Api,
    private readonly onConnect: (profile: Profile) => void,
    private readonly onError: (message: string) => void,
  ) {
    element<HTMLButtonElement>('add-profile').addEventListener('click', () => void this.newProfile());
    element<HTMLButtonElement>('add-folder').addEventListener('click', () => void this.addFolder());
    element<HTMLInputElement>('profile-search').addEventListener('input', () => this.renderProfiles());
    element<HTMLSelectElement>('auth-method').addEventListener('change', () => this.updateAuth());
    element<HTMLButtonElement>('add-private-key').addEventListener('click', () => void this.addKey());
    this.form.addEventListener('submit', (event) => {
      event.preventDefault();
      void this.save(true);
    });
    element<HTMLButtonElement>('save-profile').addEventListener('click', () => void this.save(false));
    element<HTMLFormElement>('folder-form').addEventListener('submit', (event) => {
      event.preventDefault();
      void this.createFolder();
    });
    const menu = element<HTMLDivElement>('profile-context-menu');
    document.addEventListener('click', () => { menu.hidden = true; });
    element<HTMLButtonElement>('context-edit-profile').addEventListener('click', () => {
      if (this.contextProfile) void this.editProfile(this.contextProfile);
      menu.hidden = true;
    });
    element<HTMLButtonElement>('context-add-to-folder').addEventListener('click', () => {
      void this.newProfile(this.contextFolder);
      menu.hidden = true;
    });
  }

  async open(): Promise<void> {
    this.dialog.showModal();
    await this.reload();
  }

  async newProfile(folderId = ''): Promise<void> {
    this.editingId = undefined;
    element<HTMLHeadingElement>('profile-editor-title').textContent = '新建连接';
    this.form.reset();
    this.keys = [];
    this.renderKeys('正在载入默认私钥…');
    await this.loadFolders();
    this.populateFolders();
    element<HTMLSelectElement>('profile-folder').value = folderId;
    element<HTMLSelectElement>('profile-folder').dispatchEvent(new Event('change'));
    element<HTMLSelectElement>('auth-method').dispatchEvent(new Event('change'));
    this.updateAuth();
    this.editor.showModal();
    try {
      this.keys = await this.api.defaultKeys();
      this.renderKeys();
    } catch (error) {
      this.renderKeys(messageOf(error));
    }
  }

  private async editProfile(profile: Profile): Promise<void> {
    this.editingId = profile.id;
    element<HTMLHeadingElement>('profile-editor-title').textContent = '编辑连接';
    this.form.reset();
    await this.loadFolders();
    this.populateFolders();
    setFormValue(this.form, 'name', profile.name);
    setFormValue(this.form, 'host', profile.options.host);
    setFormValue(this.form, 'port', String(profile.options.port));
    setFormValue(this.form, 'remark', profile.remark);
    setFormValue(this.form, 'folder', profile.folder);
    setFormValue(this.form, 'auth', profile.options.auth);
    setFormValue(this.form, 'user', profile.options.user);
    const password = this.form.elements.namedItem('password') as HTMLInputElement;
    password.placeholder = '留空则保留原密码';
    const passphrase = this.form.elements.namedItem('privateKeyPassphrase') as HTMLInputElement;
    passphrase.placeholder = '留空则保留原私钥短语';
    this.keys = await Promise.all(profile.options.privateKeys.map(async (path) => {
      try {
        return await this.api.inspectKey(path);
      } catch {
        return { path, name: path.split('/').pop() ?? path, type: '未知', length: 0 };
      }
    }));
    this.renderKeys();
    element<HTMLSelectElement>('profile-folder').dispatchEvent(new Event('change'));
    element<HTMLSelectElement>('auth-method').dispatchEvent(new Event('change'));
    this.updateAuth();
    this.editor.showModal();
  }

  private async reload(): Promise<void> {
    const list = element<HTMLDivElement>('profile-list');
    list.textContent = '读取连接配置…';
    try {
      [this.profiles, this.folders] = await Promise.all([this.api.profiles(), this.api.folders()]);
      this.renderProfiles();
    } catch (error) {
      list.textContent = messageOf(error);
    }
  }

  private renderProfiles(): void {
    const list = element<HTMLDivElement>('profile-list');
    list.replaceChildren();
    const query = element<HTMLInputElement>('profile-search').value.trim().toLowerCase();
    const matches = (profile: Profile) => !query || `${profile.name} ${profile.options.host} ${profile.options.user}`.toLowerCase().includes(query);
    const openSSH = this.profiles.filter((profile) => profile.source === 'openssh' && matches(profile));
    const custom = this.profiles.filter((profile) => profile.source === 'custom' && matches(profile));
    this.renderGroup(list, 'OpenSSH', openSSH);
    this.renderGroup(list, '未分组', custom.filter((profile) => !profile.folder));
    for (const folder of this.folders) {
      this.renderGroup(list, folder.name, custom.filter((profile) => profile.folder === folder.id), !query, folder.id);
    }
    if (list.children.length === 0) list.textContent = '没有匹配的连接';
  }

  private renderGroup(parent: HTMLElement, name: string, profiles: Profile[], showEmpty = false, folderId = ''): void {
    if (profiles.length === 0 && !showEmpty) return;
    const section = document.createElement('section');
    section.className = 'profile-group';
    const title = document.createElement('h3');
    title.textContent = name;
    if (folderId) {
      title.addEventListener('contextmenu', (event) => {
        event.preventDefault();
        this.showContextMenu(event, undefined, folderId);
      });
    }
    section.append(title);
    if (profiles.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'empty-state';
      empty.textContent = '空文件夹';
      section.append(empty);
    }
    for (const profile of profiles) {
      const row = document.createElement('div');
      row.className = 'profile-row';
      const connect = document.createElement('button');
      connect.className = 'profile-connect';
      const title = document.createElement('strong');
      title.textContent = profile.name;
      const host = document.createElement('span');
      host.textContent = profile.options.host;
      const port = document.createElement('span');
      port.textContent = String(profile.options.port);
      const user = document.createElement('span');
      user.textContent = profile.options.user;
      connect.append(title, host, port, user);
      connect.title = profile.remark || `${profile.options.user}@${profile.options.host}:${profile.options.port}`;
      connect.addEventListener('click', () => this.connect(profile));
      row.append(connect);
      if (profile.source === 'custom') {
        row.addEventListener('contextmenu', (event) => {
          event.preventDefault();
          this.showContextMenu(event, profile, profile.folder);
        });
      }
      {
        const remove = document.createElement('button');
        remove.className = 'link-button danger';
        remove.textContent = '删除';
        remove.addEventListener('click', () => void this.remove(profile));
        row.append(remove);
      }
      section.append(row);
    }
    parent.append(section);
  }

  private showContextMenu(event: MouseEvent, profile: Profile | undefined, folderId: string): void {
    this.contextProfile = profile;
    this.contextFolder = folderId;
    const menu = element<HTMLDivElement>('profile-context-menu');
    element<HTMLButtonElement>('context-edit-profile').hidden = !profile;
    element<HTMLButtonElement>('context-add-to-folder').hidden = !folderId;
    menu.style.left = `${Math.min(event.clientX, window.innerWidth - 170)}px`;
    menu.style.top = `${Math.min(event.clientY, window.innerHeight - 90)}px`;
    menu.hidden = false;
  }

  private connect(profile: Profile): void {
    this.dialog.close();
    this.editor.close();
    this.onConnect(profile);
  }

  private remove(profile: Profile): void {
    element<HTMLParagraphElement>('delete-profile-message').textContent = profile.source === 'openssh'
      ? `从应用中隐藏“${profile.name}”？不会修改 OpenSSH 配置。`
      : `永久删除连接“${profile.name}”？`;
    const dialog = element<HTMLDialogElement>('delete-profile-dialog');
    element<HTMLButtonElement>('delete-profile-accept').onclick = async () => {
      try {
        await this.api.deleteProfile(profile.id);
        dialog.close();
        await this.reload();
        this.onError(`已删除 ${profile.name}`);
      } catch (error) {
        this.onError(messageOf(error));
      }
    };
    element<HTMLButtonElement>('delete-profile-cancel').onclick = () => dialog.close();
    dialog.showModal();
  }

  private addFolder(): void {
    const form = element<HTMLFormElement>('folder-form');
    form.reset();
    element<HTMLDialogElement>('folder-dialog').showModal();
    (form.elements.namedItem('folderName') as HTMLInputElement).focus();
  }

  private async createFolder(): Promise<void> {
    const form = element<HTMLFormElement>('folder-form');
    const name = String(new FormData(form).get('folderName') ?? '').trim();
    if (!name) return;
    try {
      await this.api.createFolder(name);
      element<HTMLDialogElement>('folder-dialog').close();
      await this.reload();
      this.onError(`已创建文件夹 ${name}`);
    } catch (error) {
      this.onError(messageOf(error));
    }
  }

  private async loadFolders(): Promise<void> {
    this.folders = await this.api.folders();
  }

  private populateFolders(): void {
    const select = element<HTMLSelectElement>('profile-folder');
    select.replaceChildren(new Option('未分组', ''));
    for (const folder of this.folders) select.append(new Option(folder.name, folder.id));
  }

  private updateAuth(): void {
    const auth = element<HTMLSelectElement>('auth-method').value as AuthMethod;
    const password = this.form.elements.namedItem('password') as HTMLInputElement;
    const passphrase = this.form.elements.namedItem('privateKeyPassphrase') as HTMLInputElement;
    password.disabled = auth !== 'password';
    password.required = auth === 'password' && !this.editingId;
    element<HTMLButtonElement>('add-private-key').disabled = auth !== 'publicKey';
    element<HTMLDivElement>('private-key-list').classList.toggle('disabled', auth !== 'publicKey');
    passphrase.disabled = auth !== 'publicKey';
  }

  private async addKey(): Promise<void> {
    try {
      const key = await this.api.choosePrivateKey();
      if (!key) return;
      if (!this.keys.some((item) => item.path === key.path)) this.keys.push(key);
      this.renderKeys();
    } catch (error) {
      this.onError(messageOf(error));
    }
  }

  private renderKeys(message?: string): void {
    const list = element<HTMLDivElement>('private-key-list');
    list.replaceChildren();
    if (message) {
      list.textContent = message;
      return;
    }
    if (this.keys.length === 0) {
      list.textContent = '未指定私钥时使用 SSH Agent';
      return;
    }
    for (const key of this.keys) {
      const row = document.createElement('div');
      row.className = 'key-row';
      const name = document.createElement('span');
      name.textContent = key.name;
      name.title = key.path;
      const type = document.createElement('span');
      type.textContent = key.type;
      const length = document.createElement('span');
      length.textContent = String(key.length);
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'link-button danger';
      remove.textContent = '删除';
      remove.addEventListener('click', () => {
        this.keys = this.keys.filter((item) => item.path !== key.path);
        this.renderKeys();
      });
      row.append(name, type, length, remove);
      list.append(row);
    }
  }

  private async save(connectAfter: boolean): Promise<void> {
    const data = new FormData(this.form);
    const auth = String(data.get('auth')) as AuthMethod;
    const profile: Omit<Profile, 'id' | 'source'> = {
      name: String(data.get('name') ?? '').trim(),
      remark: String(data.get('remark') ?? '').trim(),
      folder: String(data.get('folder') ?? ''),
      options: {
        host: String(data.get('host') ?? '').trim(),
        port: Number(data.get('port') ?? 22),
        user: String(data.get('user') ?? '').trim(),
        auth,
        password: auth === 'password' ? String(data.get('password') ?? '') : '',
        privateKeys: auth === 'publicKey' ? this.keys.map((key) => key.path) : [],
        privateKeyPassphrase: auth === 'publicKey' ? String(data.get('privateKeyPassphrase') ?? '') : '',
      },
    };
    try {
      const created = this.editingId
        ? await this.api.updateProfile({ ...profile, id: this.editingId, source: 'custom' })
        : await this.api.createProfile(profile);
      this.editingId = undefined;
      this.editor.close();
      await this.reload();
      if (connectAfter) this.connect(created);
      else this.onError(`已保存连接 ${created.name}`);
    } catch (error) {
      this.onError(messageOf(error));
    }
  }
}

function setFormValue(form: HTMLFormElement, name: string, value: string): void {
  const field = form.elements.namedItem(name);
  if (field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement || field instanceof HTMLSelectElement) {
    field.value = value;
  }
}
