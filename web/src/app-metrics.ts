import { element, formatSize } from './utils';

interface AppMetrics {
  cpuPercent: number | null;
  memoryBytes: number;
  processCount: number;
}

export async function startAppMetrics(): Promise<void> {
  if (!('__TAURI_INTERNALS__' in window)) return;
  const output = element<HTMLOutputElement>('app-resource-usage');
  output.hidden = false;
  const { invoke } = await import('@tauri-apps/api/core');
  let pending = false;
  const refresh = async (): Promise<void> => {
    if (pending) return;
    pending = true;
    try {
      const metrics = await invoke<AppMetrics>('get_app_metrics');
      const cpu = metrics.cpuPercent === null ? '—' : `${metrics.cpuPercent.toFixed(1)}%`;
      output.value = `CPU ${cpu} · 内存 ${formatSize(metrics.memoryBytes)}`;
      output.title = `Tindomiel 全应用占用，含主进程、Go 后端和 WebKit，共 ${metrics.processCount} 个进程；每秒采样`;
    } catch {
      output.value = 'CPU — · 内存 —';
      output.title = '暂时无法读取应用资源占用';
    } finally {
      pending = false;
    }
  };
  await refresh();
  window.setInterval(() => void refresh(), 1000);
}
