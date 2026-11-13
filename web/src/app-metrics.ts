import { recordFrontendLog } from './logs';
import { element, formatSize, messageOf } from './utils';

interface AppProcessUsage {
  pid: number;
  cpuTimeNs: number;
  memoryBytes: number;
}

interface AppMetrics {
  cpuPercent: number | null;
  memoryBytes: number;
  processCount: number;
  cpuTimeNs: number;
  previousCpuTimeNs: number | null;
  sampleIntervalNs: number | null;
  processes: AppProcessUsage[];
}

const summaryIntervalMs = 5 * 60 * 1000;
const suspendGapMs = 5000;
const detailedLogThrottleMs = 30 * 1000;

export async function startAppMetrics(): Promise<void> {
  if (!('__TAURI_INTERNALS__' in window)) return;
  const output = element<HTMLOutputElement>('app-resource-usage');
  output.hidden = false;
  const { invoke } = await import('@tauri-apps/api/core');
  let pending = false;
  let queuedReason: string | undefined;
  let previousSampleAt = 0;
  let lastSummaryAt = 0;
  let lastDetailedAt = 0;
  let lastErrorAt = 0;
  const refresh = async (reason = 'periodic'): Promise<void> => {
    if (pending) {
      if (reason !== 'periodic') queuedReason = reason;
      return;
    }
    pending = true;
    try {
      const metrics = await invoke<AppMetrics>('get_app_metrics');
      const sampledAt = Date.now();
      const wallGapMs = previousSampleAt > 0 ? sampledAt - previousSampleAt : 0;
      previousSampleAt = sampledAt;
      const cpu = metrics.cpuPercent === null ? '—' : `${metrics.cpuPercent.toFixed(1)}%`;
      output.value = `CPU ${cpu} · 内存 ${formatSize(metrics.memoryBytes)}`;
      output.title = `Tindomiel 全应用占用，含主进程、Go 后端和 WebKit，共 ${metrics.processCount} 个进程；每秒采样`;
      const anomalous = (metrics.cpuPercent !== null && (!Number.isFinite(metrics.cpuPercent) || metrics.cpuPercent < 0 || metrics.cpuPercent > 1_000_000))
        || !Number.isSafeInteger(metrics.memoryBytes) || metrics.memoryBytes <= 0 || metrics.processCount !== metrics.processes.length;
      const detailed = reason === 'startup' || wallGapMs > suspendGapMs || anomalous
        || (reason !== 'periodic' && sampledAt - lastDetailedAt >= detailedLogThrottleMs);
      if (detailed || sampledAt - lastSummaryAt >= summaryIntervalMs) {
        const summary = `本机资源采样 reason=${reason} wallGapMs=${wallGapMs} sampleIntervalNs=${metrics.sampleIntervalNs ?? 'null'} cpuPercent=${metrics.cpuPercent ?? 'null'} cpuTimeNs=${metrics.cpuTimeNs} previousCpuTimeNs=${metrics.previousCpuTimeNs ?? 'null'} memoryBytes=${metrics.memoryBytes} processCount=${metrics.processCount}`;
        const details = detailed ? ` processes=${JSON.stringify(metrics.processes)}` : '';
        recordFrontendLog(summary + details, anomalous ? 'warn' : 'info');
        lastSummaryAt = sampledAt;
        if (detailed) lastDetailedAt = sampledAt;
      }
    } catch (error) {
      output.value = 'CPU — · 内存 —';
      output.title = '暂时无法读取应用资源占用';
      const failedAt = Date.now();
      if (failedAt - lastErrorAt >= detailedLogThrottleMs) {
        recordFrontendLog(`本机资源采样失败 reason=${reason}: ${messageOf(error)}`, 'error');
        lastErrorAt = failedAt;
      }
    } finally {
      pending = false;
      if (queuedReason) {
        const nextReason = queuedReason;
        queuedReason = undefined;
        void refresh(nextReason);
      }
    }
  };
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) void refresh('window-visible');
  });
  window.addEventListener('focus', () => void refresh('window-focus'));
  window.addEventListener('pageshow', () => void refresh('pageshow'));
  await refresh('startup');
  window.setInterval(() => void refresh(), 1000);
}
