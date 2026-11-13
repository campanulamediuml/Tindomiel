import { ApiError, type AppSettings, type FileEntry, type Folder, type KeyInfo, type Profile, type ResourceMetrics, type SessionInfo } from './types';

interface BackendInfo {
  url: string;
  token: string;
}

interface ErrorBody {
  error?: { code?: string; message?: string; details?: unknown };
}

export class Api {
  private constructor(private readonly baseUrl: string, private readonly token: string) {}

  static async create(): Promise<Api> {
    const info = await backendInfo();
    return new Api(info.url.replace(/\/$/, ''), info.token);
  }

  recentLogs(lines = 200): Promise<{ log: string; path: string }> {
    return this.request('GET', `/api/logs/recent?lines=${lines}`);
  }

  recentFrontendLogs(lines = 200): Promise<{ log: string; path: string }> {
    return this.request('GET', `/api/logs/frontend/recent?lines=${lines}`);
  }

  recordLog(message: string, level = 'info'): Promise<void> {
    return this.request('POST', '/api/logs/frontend', { message, level });
  }

  defaultDownloadPath(): Promise<{ path: string }> {
    return this.request('GET', '/api/local/download-path');
  }

  getSettings(): Promise<AppSettings> {
    return this.request('GET', '/api/settings');
  }

  updateSettings(settings: AppSettings): Promise<AppSettings> {
    return this.request('PUT', '/api/settings', settings);
  }

  openDownloadDirectory(): Promise<void> {
    return this.request('POST', '/api/local/open-downloads');
  }

  async chooseDownloadDirectory(): Promise<string | undefined> {
    try {
      const result = await this.request<{ path: string }>('POST', '/api/local/choose-download-directory');
      return result.path;
    } catch (error) {
      if (error instanceof ApiError && error.code === 'DIRECTORY_SELECTION_CANCELLED') return undefined;
      throw error;
    }
  }

  profiles(): Promise<Profile[]> {
    return this.request('GET', '/api/profiles');
  }

  folders(): Promise<Folder[]> {
    return this.request('GET', '/api/folders');
  }

  createFolder(name: string): Promise<Folder> {
    return this.request('POST', '/api/folders', { name });
  }

  createProfile(profile: Omit<Profile, 'id' | 'source'>): Promise<Profile> {
    return this.request('POST', '/api/profiles', profile);
  }

  updateProfile(profile: Profile): Promise<Profile> {
    return this.request('PUT', `/api/profiles/${encodeURIComponent(profile.id)}`, profile);
  }

  deleteProfile(profileId: string): Promise<void> {
    return this.request('DELETE', `/api/profiles/${encodeURIComponent(profileId)}`);
  }

  inspectKey(path: string): Promise<KeyInfo> {
    return this.request('POST', '/api/keys/info', { path });
  }

  defaultKeys(): Promise<KeyInfo[]> {
    return this.request('GET', '/api/keys/defaults');
  }

  async choosePrivateKey(): Promise<KeyInfo | undefined> {
    try {
      const selected = await this.request<{ path: string }>('POST', '/api/local/choose-file');
      return await this.request<KeyInfo>('POST', '/api/keys/info', selected);
    } catch (error) {
      if (error instanceof ApiError && error.code === 'FILE_SELECTION_CANCELLED') return undefined;
      throw error;
    }
  }

  createSession(profileId: string, cols = 120, rows = 32): Promise<SessionInfo> {
    return this.request('POST', '/api/sessions', { profileId, cols, rows });
  }

  deleteSession(sessionId: string): Promise<void> {
    return this.request('DELETE', `/api/sessions/${encodeURIComponent(sessionId)}`);
  }

  reconnect(sessionId: string): Promise<void> {
    return this.request('POST', `/api/sessions/${encodeURIComponent(sessionId)}/reconnect`);
  }

  metrics(sessionId: string): Promise<ResourceMetrics> {
    return this.request('GET', `/api/metrics/${encodeURIComponent(sessionId)}`);
  }

  acceptHostKey(verificationId: string, fingerprint: string): Promise<void> {
    return this.request('POST', '/api/host-keys/accept', { verificationId, fingerprint });
  }

  listFiles(sessionId: string, remotePath: string): Promise<FileEntry[]> {
    return this.request('GET', `/api/sftp/${encodeURIComponent(sessionId)}/list?path=${encodeURIComponent(remotePath)}`);
  }

  async realpath(sessionId: string, remotePath: string): Promise<string> {
    const result = await this.request<{ path: string }>(
      'GET',
      `/api/sftp/${encodeURIComponent(sessionId)}/realpath?path=${encodeURIComponent(remotePath)}`,
    );
    return result.path;
  }

  openRemote(sessionId: string, remotePath: string, application = ''): Promise<{ cachePath: string }> {
    return this.request('POST', `/api/sftp/${encodeURIComponent(sessionId)}/open`, { path: remotePath, application });
  }

  async chooseApplication(): Promise<string | undefined> {
    try {
      const result = await this.request<{ path: string }>('POST', '/api/local/choose-application');
      return result.path;
    } catch (error) {
      if (error instanceof ApiError && error.code === 'FILE_SELECTION_CANCELLED') return undefined;
      throw error;
    }
  }

  upload(
    sessionId: string,
    directory: string,
    file: File,
    onProgress: (loaded: number, total: number) => void,
  ): Promise<void> {
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      const query = new URLSearchParams({ path: directory });
      request.open('POST', `${this.baseUrl}/api/sftp/${encodeURIComponent(sessionId)}/upload?${query}`);
      request.setRequestHeader('Authorization', `Bearer ${this.token}`);
      request.upload.onprogress = (event) => {
        if (event.lengthComputable) onProgress(event.loaded, event.total);
      };
      request.onerror = () => reject(new ApiError(0, 'NETWORK_ERROR', '上传请求失败'));
      request.onload = () => {
        const body = parseJSON(request.responseText) as ErrorBody;
        if (request.status >= 200 && request.status < 300) {
          resolve();
          return;
        }
        reject(new ApiError(request.status, body.error?.code ?? 'UPLOAD_FAILED', body.error?.message ?? '上传失败', body.error?.details));
      };
      const form = new FormData();
      form.append('file', file, file.name);
      request.send(form);
    });
  }

  download(sessionId: string, remotePath: string): Promise<{ path: string; bytes: number }> {
    return this.request('POST', `/api/sftp/${encodeURIComponent(sessionId)}/download-file`, { path: remotePath });
  }

  downloadDirectory(sessionId: string, remotePath: string): Promise<{ path: string; files: number; bytes: number }> {
    return this.request('POST', `/api/sftp/${encodeURIComponent(sessionId)}/download-directory`, { path: remotePath });
  }

  async terminalUrl(sessionId: string): Promise<string> {
    const result = await this.request<{ ticket: string }>('POST', `/api/sessions/${encodeURIComponent(sessionId)}/ws-ticket`);
    const base = this.baseUrl.replace(/^http/, 'ws');
    const query = new URLSearchParams({ sessionId, ticket: result.ticket });
    return `${base}/ws/terminal?${query}`;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${this.token}`,
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await response.text();
    const result = (text ? parseJSON(text) : {}) as T & ErrorBody;
    if (!response.ok) {
      throw new ApiError(response.status, result.error?.code ?? 'REQUEST_FAILED', result.error?.message ?? `请求失败 (${response.status})`, result.error?.details);
    }
    return result;
  }
}

async function backendInfo(): Promise<BackendInfo> {
  if ('__TAURI_INTERNALS__' in window) {
    const { invoke } = await import('@tauri-apps/api/core');
    return invoke<BackendInfo>('get_backend_info');
  }
  const url = import.meta.env.VITE_BACKEND_URL ?? 'http://127.0.0.1:18080';
  let token = import.meta.env.VITE_BACKEND_TOKEN ?? sessionStorage.getItem('tindomiel.token');
  if (!token) token = window.prompt('请输入 Go 后端启动时输出的访问 token') ?? '';
  if (!token) throw new Error('缺少后端访问 token');
  sessionStorage.setItem('tindomiel.token', token);
  return { url, token };
}

function parseJSON(text: string): unknown {
  try {
    return text ? JSON.parse(text) : {};
  } catch {
    return {};
  }
}
