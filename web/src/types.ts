export type SessionStatus = 'connecting' | 'ready' | 'disconnected' | 'reconnecting' | 'closed' | 'error';
export type DisplayStatus = SessionStatus | 'detached';
export type AuthMethod = 'publicKey' | 'password';

export interface ShortcutMapping {
  from: string;
  to: string;
}

export type TerminalTheme = 'homebrew' | 'clearDark' | 'boring';

export interface AppSettings {
  theme: TerminalTheme;
  fontSize: number;
  cursorBlink: boolean;
  scrollback: number;
  downloadDirectory: string;
  shortcuts: ShortcutMapping[];
}

export interface ProfileOptions {
  host: string;
  port: number;
  user: string;
  auth: AuthMethod;
  password?: string;
  privateKeys: string[];
  privateKeyPassphrase?: string;
  shortcuts?: ShortcutMapping[];
}

export interface Profile {
  id: string;
  name: string;
  remark: string;
  folder: string;
  source: 'openssh' | 'custom';
  options: ProfileOptions;
}

export interface Folder {
  id: string;
  name: string;
}

export interface KeyInfo {
  path: string;
  name: string;
  type: string;
  length: number;
}

export interface SessionInfo {
  sessionId: string;
  profileId: string;
  status: SessionStatus;
  openedAt: number;
}

export interface HostKeyDetails {
  verificationId: string;
  host: string;
  port: number;
  keyType: string;
  fingerprint: string;
}

export interface FileEntry {
  name: string;
  type: 'file' | 'directory' | 'symlink';
  size: number;
  mtime: number;
  created: number;
  permissions: string;
}

export interface ProcessMetrics {
  pid: number;
  cpuPercent: number;
  memoryBytes: number;
  command: string;
}

export interface ResourceMetrics {
  system: {
    hostname: string;
    osName: string;
    kernelVersion: string;
    architecture: string;
    cpuModel: string;
    cpuCores: number;
  };
  uptimeSeconds: number;
  loadAverage: number[];
  cpuPercent: number;
  memory: { total: number; used: number; available: number; usedPercent: number };
  swap: { total: number; used: number; usedPercent: number };
  processes: ProcessMetrics[];
  interfaces: { name: string; rxBytesPerSecond: number; txBytesPerSecond: number }[];
  pingMs: number;
  disks: { path: string; total: number; used: number; available: number; usedPercent: number }[];
}

export interface TransferState {
  id: string;
  name: string;
  direction: 'upload' | 'download';
  path: string;
  currentPath?: string;
  loaded: number;
  total: number;
  status: 'running' | 'complete' | 'failed';
  error?: string;
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly details?: unknown,
  ) {
    super(message);
  }
}
