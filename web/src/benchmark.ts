import { MetricsPanel } from './metrics';
import { SftpPanel } from './sftp';
import { TerminalView } from './terminal';
import { TransferCenter } from './transfer';
import type { FileEntry, ResourceMetrics, SessionInfo, TransferState } from './types';

interface MemoryPerformance extends Performance {
  memory?: { usedJSHeapSize: number };
}

interface BenchmarkResult {
  status: string;
  metricsMs: number;
  transferMs: number;
  transferFlushMs: number;
  sftpMs: number;
  processRowCount: number;
  transferRowCount: number;
  fileRowCount: number;
  heapBefore?: number;
  heapAfter?: number;
  reconnectStartsAtColumnZero: boolean;
  reconnectLineBreakCount: number;
  hiddenWebglReleasedWithoutDom: boolean;
  webglFallbackWorks: boolean;
  webglRestored: boolean;
  hiddenOutputPreserved: boolean;
  processRowsReused: boolean;
  transferRowsReused: boolean;
  fileDatesMatch: boolean;
}

async function installWorkspace(): Promise<void> {
  const response = await fetch('/');
  const parsed = new DOMParser().parseFromString(await response.text(), 'text/html');
  parsed.querySelectorAll('script').forEach((script) => script.remove());
  document.body.replaceChildren();
  for (const node of Array.from(parsed.body.childNodes)) document.body.append(node.cloneNode(true));
}

function sampleMetrics(): ResourceMetrics {
  return {
    timestamp: Math.floor(Date.now() / 1000),
    system: { hostname: 'benchmark', osName: 'Linux', kernelVersion: '1', architecture: 'arm64', cpuModel: 'CPU', cpuCores: 8 },
    uptimeSeconds: 1000,
    loadAverage: [1, 2, 3],
    cpuPercent: 25,
    memory: { total: 1000, used: 500, available: 500, usedPercent: 50 },
    swap: { total: 1000, used: 100, usedPercent: 10 },
    processes: Array.from({ length: 200 }, (_, index) => ({ pid: index + 1, cpuPercent: index % 37, memoryBytes: (index + 1) * 1000, command: `process-${index}` })),
    interfaces: [{ name: 'eth0', rxBytesPerSecond: 100, txBytesPerSecond: 200 }],
    pingMs: 10,
    disks: Array.from({ length: 8 }, (_, index) => ({ path: `/disk-${index}`, total: 1000, used: index * 100, available: 1000 - index * 100, usedPercent: index * 10 })),
  };
}

function sampleFiles(): FileEntry[] {
  return Array.from({ length: 500 }, (_, index) => ({
    name: `${index % 2 === 0 ? 'file' : 'directory'}-${String(index).padStart(4, '0')}`,
    type: index % 2 === 0 ? 'file' : 'directory',
    size: index * 100,
    mtime: 1731200000 + index,
    created: 1731100000 + index,
    permissions: '-rw-r--r--',
  }));
}

function median(values: number[]): number {
  return [...values].sort((left, right) => left - right)[Math.floor(values.length / 2)] ?? 0;
}

async function terminalReconnectCheck(): Promise<{ startsAtZero: boolean; breaks: number; hiddenReleased: boolean; fallback: boolean; restored: boolean; hiddenOutput: boolean }> {
  const sockets: { onmessage: ((event: MessageEvent) => void) | null }[] = [];
  class MockWebSocket {
    onmessage: ((event: MessageEvent) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: (() => void) | null = null;
    binaryType = 'arraybuffer';
    constructor(_url: string) { sockets.push(this); }
    close(): void { }
    send(_data: unknown): void { }
  }
  Object.defineProperty(window, 'WebSocket', { configurable: true, value: MockWebSocket });
  const session: SessionInfo = { sessionId: 'benchmark', profileId: 'benchmark', status: 'ready', openedAt: 0 };
  const api = { terminalUrl: async () => 'ws://127.0.0.1/benchmark' };
  const view = new TerminalView(session, api as never, () => undefined, () => undefined, () => undefined, () => undefined, () => undefined, () => undefined, { theme: 'homebrew', fontSize: 13, cursorBlink: true, scrollback: 10_000, downloadDirectory: '', shortcuts: [] });
  document.getElementById('terminal-area')?.append(view.element);
  view.status = 'disconnected';
  await view.connect();
  await new Promise<void>((resolve) => view.terminal.write('user@host:~$ ', resolve));
  const before = view.terminal.buffer.active.cursorY;
  const status = JSON.stringify({ type: 'status', status: 'reconnecting' });
  sockets[0]?.onmessage?.({ data: status } as MessageEvent);
  sockets[0]?.onmessage?.({ data: status } as MessageEvent);
  await new Promise<void>((resolve) => view.terminal.write('Welcome to Ubuntu', resolve));
  const welcomeLine = view.terminal.buffer.active.getLine(before + 1)?.translateToString(true) ?? '';
  const breaks = view.terminal.buffer.active.cursorY - before;
  view.deactivate();
  (view as unknown as { releaseWebgl(): void }).releaseWebgl();
  const hiddenReleased = view.element.querySelector('.xterm-screen .xterm-rows') === null && view.element.querySelector('.xterm-screen canvas') === null;
  (view.terminal as unknown as { _core: { writeSync(data: string): void } })._core.writeSync('\r\nhidden-output');
  view.activate();
  const restored = view.element.querySelector('.xterm-screen canvas') !== null;
  const buffer = view.terminal.buffer.active;
  const hiddenOutput = buffer.getLine(buffer.baseY + buffer.cursorY)?.translateToString(true).includes('hidden-output') ?? false;
  (view as unknown as { releaseWebgl(): void }).releaseWebgl();
  const fallback = view.element.querySelector('.xterm-screen .xterm-rows') !== null && view.element.querySelector('.xterm-screen canvas') === null;
  const result = { startsAtZero: welcomeLine.startsWith('Welcome to Ubuntu'), breaks, hiddenReleased, fallback, restored, hiddenOutput };
  try {
    view.dispose();
  } catch {
    // WebGL teardown is outside this benchmark's scope.
  }
  return result;
}

async function run(): Promise<void> {
  await installWorkspace();
  const heap = (performance as MemoryPerformance).memory;
  const heapBefore = heap?.usedJSHeapSize;
  const reconnect = await terminalReconnectCheck();

  const metricsPanel = new MetricsPanel({ metrics: async () => sampleMetrics() } as never, () => undefined);
  Object.assign(metricsPanel, { sessionId: 'metrics', status: 'ready' });
  const metricsSamples: number[] = [];
  let firstProcessRow: Element | null = null;
  for (let sample = 0; sample < 5; sample += 1) {
    for (let index = 0; index < 10; index += 1) metricsPanel.receive('metrics', sampleMetrics());
    if (sample === 0) firstProcessRow = document.querySelector('#process-list .process-row');
    const started = performance.now();
    for (let index = 0; index < 100; index += 1) metricsPanel.receive('metrics', { ...sampleMetrics(), cpuPercent: index % 100 });
    metricsSamples.push(performance.now() - started);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }

  const transferCenter = new TransferCenter();
  const popover = document.getElementById('transfer-popover');
  if (popover) popover.hidden = false;
  const transfers: TransferState[] = Array.from({ length: 60 }, (_, index) => ({ id: `transfer-${index}`, name: `file-${index}`, direction: 'download', path: `/file-${index}`, loaded: 0, total: 1000, status: 'running' }));
  transfers.forEach((transfer) => transferCenter.update(transfer));
  (transferCenter as unknown as { flush(): void }).flush();
  const firstTransferRow = document.querySelector('#global-transfer-list .transfer-row');
  const transferSamples: number[] = [];
  for (let sample = 0; sample < 5; sample += 1) {
    transfers.forEach((transfer) => transferCenter.update(transfer));
    const started = performance.now();
    for (let index = 0; index < 500; index += 1) transferCenter.update({ ...transfers[index % transfers.length]!, loaded: index % 1000 });
    transferSamples.push(performance.now() - started);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  const transferFlushStarted = performance.now();
  (transferCenter as unknown as { flush(): void }).flush();
  const transferFlushMs = performance.now() - transferFlushStarted;

  const sftp = new SftpPanel({} as never, transferCenter, () => undefined);
  Object.assign(sftp, { entries: sampleFiles(), currentPath: '/benchmark' });
  const sftpSamples: number[] = [];
  for (let sample = 0; sample < 5; sample += 1) {
    (sftp as unknown as { render(): void }).render();
    const started = performance.now();
    for (let index = 0; index < 20; index += 1) (sftp as unknown as { render(): void }).render();
    sftpSamples.push(performance.now() - started);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  const firstFileDate = document.querySelector('#file-list .file-row span:nth-child(3)')?.textContent;
  const expectedFileDate = new Date((sampleFiles()[1]?.mtime ?? 0) * 1000).toLocaleString('zh-CN', { hour12: false });

  const result: BenchmarkResult = {
    status: 'benchmark-complete',
    metricsMs: median(metricsSamples),
    transferMs: median(transferSamples),
    transferFlushMs,
    sftpMs: median(sftpSamples),
    processRowCount: document.querySelectorAll('#process-list .process-row').length,
    transferRowCount: document.querySelectorAll('#global-transfer-list .transfer-row').length,
    fileRowCount: document.querySelectorAll('#file-list .file-row').length,
    heapBefore,
    heapAfter: heap?.usedJSHeapSize,
    reconnectStartsAtColumnZero: reconnect.startsAtZero,
    reconnectLineBreakCount: reconnect.breaks,
    hiddenWebglReleasedWithoutDom: reconnect.hiddenReleased,
    webglFallbackWorks: reconnect.fallback,
    webglRestored: reconnect.restored,
    hiddenOutputPreserved: reconnect.hiddenOutput,
    processRowsReused: firstProcessRow !== null && firstProcessRow === document.querySelector('#process-list .process-row'),
    transferRowsReused: firstTransferRow !== null && firstTransferRow === document.querySelector('#global-transfer-list .transfer-row'),
    fileDatesMatch: firstFileDate === expectedFileDate,
  };
  document.body.innerHTML = `<pre id="benchmark-results">${JSON.stringify(result, null, 2)}</pre>`;
}

void run().catch((error) => {
  document.body.innerHTML = `<pre id="benchmark-results">benchmark-failed: ${error instanceof Error ? error.stack : String(error)}</pre>`;
});
