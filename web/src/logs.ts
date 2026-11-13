import type { Api } from './api';
import { element, messageOf } from './utils';

let persistLog: (message: string, level: string) => void = () => undefined;

export class LogCenter {
  private activeTab: 'backend' | 'frontend' = 'backend';

  constructor(private readonly api: Api) {
    persistLog = (message, level) => {
      void api.recordLog(message, level).catch(() => undefined);
    };
    element<HTMLButtonElement>('logs-button').addEventListener('click', () => void this.open());
    element<HTMLButtonElement>('refresh-logs').addEventListener('click', () => void this.load());
    element<HTMLButtonElement>('backend-log-tab').addEventListener('click', () => this.selectTab('backend'));
    element<HTMLButtonElement>('frontend-log-tab').addEventListener('click', () => this.selectTab('frontend'));
    document.addEventListener('tindomiel:frontend-log', () => {
      if (this.activeTab === 'frontend') void this.load();
    });
  }

  private async open(): Promise<void> {
    element<HTMLDialogElement>('logs-dialog').showModal();
    await this.load();
  }

  private selectTab(tab: 'backend' | 'frontend'): void {
    this.activeTab = tab;
    element<HTMLButtonElement>('backend-log-tab').classList.toggle('active', tab === 'backend');
    element<HTMLButtonElement>('frontend-log-tab').classList.toggle('active', tab === 'frontend');
    void this.load();
  }

  private async load(): Promise<void> {
    const content = element<HTMLPreElement>('log-content');
    content.textContent = '读取中…';
    try {
      const result = this.activeTab === 'backend'
        ? await this.api.recentLogs(300)
        : await this.api.recentFrontendLogs(300);
      element<HTMLDivElement>('log-path').textContent = result.path;
      content.textContent = result.log || '暂无日志';
    } catch (error) {
      content.textContent = messageOf(error);
    }
  }
}

export function recordFrontendLog(message: string, level = 'info'): void {
  persistLog(message, level);
  document.dispatchEvent(new CustomEvent('tindomiel:frontend-log'));
}
