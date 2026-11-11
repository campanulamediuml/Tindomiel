import type { Api } from './api';
import type { DisplayStatus, ResourceMetrics } from './types';
import { element, formatDuration, formatRate, formatSize } from './utils';

interface NetworkHistory {
  rx: number[];
  tx: number[];
}

interface SessionHistory {
  network: Map<string, NetworkHistory>;
  ping: number[];
  selectedInterface: string;
}

interface ProcessRow {
  root: HTMLDivElement;
  memory: HTMLSpanElement;
  cpu: HTMLSpanElement;
  command: HTMLSpanElement;
}

interface DiskRow {
  root: HTMLDivElement;
  size: HTMLSpanElement;
  bar: HTMLDivElement;
}

export class MetricsPanel {
  private sessionId?: string;
  private status?: DisplayStatus;
  private readonly snapshots = new Map<string, ResourceMetrics>();
  private sortKey: 'cpu' | 'memory' = 'cpu';
  private sortDirection = -1;
  private selectedInterface = '';
  private interfaceSignature = '';
  private readonly histories = new Map<string, SessionHistory>();
  private readonly onError: (message: string) => void;
  private host = '';
  private latest?: ResourceMetrics;
  private activeInterface = '';
  private networkAnimationFrame = 0;
  private displayedNetwork: NetworkHistory = { rx: [], tx: [] };
  private sessionVersion = 0;
  private readonly processRows: ProcessRow[] = [];
  private readonly diskRows = new Map<string, DiskRow>();

  constructor(private readonly api: Api, onError: (message: string) => void) {
    this.onError = onError;
    element<HTMLButtonElement>('sort-cpu').addEventListener('click', () => this.changeSort('cpu'));
    element<HTMLButtonElement>('sort-memory').addEventListener('click', () => this.changeSort('memory'));
    element<HTMLSelectElement>('network-interface').addEventListener('change', (event) => {
      this.selectedInterface = (event.currentTarget as HTMLSelectElement).value;
      this.drawNetwork();
    });
    element<HTMLButtonElement>('copy-ip').addEventListener('click', () => {
      const host = element<HTMLElement>('target-ip').dataset.host;
      if (host) void navigator.clipboard.writeText(host).then(() => this.onError(`已复制 ${host}`));
    });
    element<HTMLButtonElement>('system-info-button').addEventListener('click', () => {
      this.renderDialog();
      element<HTMLDialogElement>('metrics-dialog').showModal();
    });
    new ResizeObserver(() => {
      this.drawNetwork();
      this.drawPing();
    }).observe(element<HTMLElement>('metrics-panel'));
  }

  setSession(sessionId: string | undefined, host = '', status?: DisplayStatus): void {
    if (this.sessionId !== sessionId) {
      if (this.sessionId) this.historyFor(this.sessionId).selectedInterface = this.selectedInterface || 'auto';
      this.sessionId = sessionId;
      this.sessionVersion += 1;
      this.selectedInterface = sessionId ? this.historyFor(sessionId).selectedInterface : 'auto';
      this.interfaceSignature = '';
      this.clearView();
    }
    this.host = host;
    element<HTMLElement>('target-ip').textContent = host ? `IP ${host}` : 'IP —';
    element<HTMLElement>('target-ip').dataset.host = host;
    this.setStatus(status);
  }

  setStatus(status?: DisplayStatus): void {
    this.status = status;
    if (status !== 'ready' || !this.sessionId) return;
    const cached = this.snapshots.get(this.sessionId);
    if (cached) this.render(cached);
    else void this.loadCached();
  }

  receive(sessionId: string, metrics: ResourceMetrics): void {
    this.snapshots.set(sessionId, metrics);
    this.recordHistory(sessionId, metrics);
    if (this.sessionId === sessionId && this.status === 'ready') this.render(metrics);
  }

  removeSession(sessionId: string): void {
    this.snapshots.delete(sessionId);
    this.histories.delete(sessionId);
  }

  private historyFor(sessionId: string): SessionHistory {
    let history = this.histories.get(sessionId);
    if (!history) {
      history = { network: new Map(), ping: [], selectedInterface: 'auto' };
      this.histories.set(sessionId, history);
    }
    return history;
  }

  private recordHistory(sessionId: string, metrics: ResourceMetrics): void {
    const history = this.historyFor(sessionId);
    for (const iface of metrics.interfaces) {
      const network = history.network.get(iface.name) ?? { rx: [], tx: [] };
      network.rx.push(iface.rxBytesPerSecond);
      network.tx.push(iface.txBytesPerSecond);
      if (network.rx.length > 40) network.rx.shift();
      if (network.tx.length > 40) network.tx.shift();
      history.network.set(iface.name, network);
    }
    history.ping.push(metrics.pingMs);
    if (history.ping.length > 40) history.ping.shift();
  }

  private async loadCached(): Promise<void> {
    const sessionId = this.sessionId;
    const version = this.sessionVersion;
    if (!sessionId) return;
    try {
      const metrics = await this.api.metrics(sessionId);
      this.snapshots.set(sessionId, metrics);
      this.recordHistory(sessionId, metrics);
      if (this.sessionId === sessionId && this.sessionVersion === version && this.status === 'ready') this.render(metrics);
    } catch {
      if (this.sessionId === sessionId && this.sessionVersion === version && this.status === 'ready') {
        element<HTMLDivElement>('system-load').textContent = '监控暂不可用';
      }
    }
  }

  private render(metrics: ResourceMetrics): void {
    this.latest = metrics;
    element<HTMLDivElement>('system-uptime').textContent = `运行 ${formatDuration(metrics.uptimeSeconds)}`;
    element<HTMLDivElement>('system-load').textContent = `负载 ${metrics.loadAverage.length ? metrics.loadAverage.map((value) => value.toFixed(2)).join(', ') : '—'}`;
    element<HTMLSpanElement>('cpu-text').textContent = `${metrics.cpuPercent.toFixed(1)}%`;
    element<HTMLSpanElement>('memory-text').textContent = `${formatSize(metrics.memory.used)} / ${formatSize(metrics.memory.total)}`;
    element<HTMLSpanElement>('swap-text').textContent = `${formatSize(metrics.swap.used)} / ${formatSize(metrics.swap.total)}`;
    setMeter('cpu-bar', metrics.cpuPercent);
    setMeter('memory-bar', metrics.memory.usedPercent);
    setMeter('swap-bar', metrics.swap.usedPercent);
    this.renderProcesses(metrics);
    this.renderNetwork(metrics);
    element<HTMLSpanElement>('ping-text').textContent = metrics.pingMs > 0 ? `${metrics.pingMs.toFixed(1)} ms` : '— ms';
    this.drawPing();
    this.renderDisks(metrics);
    if (element<HTMLDialogElement>('metrics-dialog').open) this.renderDialog();
  }

  private renderDialog(): void {
    const content = element<HTMLDivElement>('metrics-dialog-content');
    content.replaceChildren();
    const metrics = this.latest;
    if (!metrics) {
      content.textContent = '连接后显示系统信息';
      return;
    }
    const summary = document.createElement('div');
    summary.className = 'metrics-summary';
    addMetricCard(summary, 'CPU', `${metrics.cpuPercent.toFixed(1)}%`, metrics.cpuPercent);
    addMetricCard(summary, '内存', `${formatSize(metrics.memory.used)} / ${formatSize(metrics.memory.total)}`, metrics.memory.usedPercent);
    addMetricCard(summary, '交换', `${formatSize(metrics.swap.used)} / ${formatSize(metrics.swap.total)}`, metrics.swap.usedPercent);
    addMetricCard(summary, '延迟', metrics.pingMs > 0 ? `${metrics.pingMs.toFixed(1)} ms` : '—', 0);
    content.append(summary);

    const system = document.createElement('div');
    system.className = 'dialog-system-line';
    system.textContent = `IP ${this.host || '—'}　运行 ${formatDuration(metrics.uptimeSeconds)}　负载 ${metrics.loadAverage.map((value) => value.toFixed(2)).join(', ') || '—'}`;
    content.append(system);

    const detailsTitle = document.createElement('h3');
    detailsTitle.textContent = '系统详情';
    content.append(detailsTitle);
    const details = document.createElement('div');
    details.className = 'system-details';
    const entries = [
      ['主机名', metrics.system.hostname],
      ['操作系统', metrics.system.osName],
      ['内核版本', metrics.system.kernelVersion],
      ['系统架构', metrics.system.architecture],
      ['CPU 型号', metrics.system.cpuModel],
      ['逻辑核数', String(metrics.system.cpuCores)],
    ];
    for (const [label = '', value = ''] of entries) {
      const row = document.createElement('div');
      const name = textSpan(label);
      const content = textSpan(value || '—');
      row.append(name, content);
      details.append(row);
    }
    content.append(details);

    const processTitle = document.createElement('h3');
    processTitle.textContent = '进程资源占用';
    content.append(processTitle);
    const processes = document.createElement('div');
    processes.className = 'dialog-processes';
    const processesHeader = document.createElement('div');
    processesHeader.className = 'dialog-process-row header';
    processesHeader.append(textSpan('内存'), textSpan('CPU'), textSpan('命令'));
    processes.append(processesHeader);
    const sorted = [...metrics.processes].sort((left, right) => {
      const a = this.sortKey === 'cpu' ? left.cpuPercent : left.memoryBytes;
      const b = this.sortKey === 'cpu' ? right.cpuPercent : right.memoryBytes;
      return (a - b) * this.sortDirection;
    }).slice(0, 20);
    const maxMemory = Math.max(1, ...metrics.processes.map((process) => process.memoryBytes));
    const maxCPU = Math.max(1, ...metrics.processes.map((process) => process.cpuPercent));
    for (const process of sorted) {
      const row = document.createElement('div');
      row.className = 'dialog-process-row';
      const memory = textSpan(formatSize(process.memoryBytes));
      memory.style.background = barBackground(process.memoryBytes, maxMemory, '76, 141, 255');
      const cpu = textSpan(process.cpuPercent.toFixed(1));
      cpu.style.background = barBackground(process.cpuPercent, maxCPU, '63, 185, 80');
      row.append(memory, cpu, textSpan(process.command));
      processes.append(row);
    }
    content.append(processes);

    const networkTitle = document.createElement('h3');
    networkTitle.textContent = '网络接口';
    content.append(networkTitle);
    const network = document.createElement('div');
    network.className = 'dialog-network';
    for (const iface of metrics.interfaces) {
      const row = document.createElement('div');
      row.append(textSpan(iface.name), textSpan(`↑ ${formatRate(iface.txBytesPerSecond)}　↓ ${formatRate(iface.rxBytesPerSecond)}`));
      network.append(row);
    }
    content.append(network);

    const diskTitle = document.createElement('h3');
    diskTitle.textContent = '磁盘空间';
    content.append(diskTitle);
    const disks = document.createElement('div');
    disks.className = 'dialog-disks';
    for (const disk of metrics.disks) {
      const row = document.createElement('div');
      row.className = 'dialog-disk-row';
      const labels = document.createElement('div');
      labels.append(textSpan(disk.path), textSpan(`${formatSize(disk.used)} / ${formatSize(disk.total)}`));
      const meter = document.createElement('div');
      meter.className = 'meter';
      const bar = document.createElement('div');
      bar.style.width = `${Math.min(100, Math.max(0, disk.usedPercent))}%`;
      meter.append(bar);
      row.append(labels, meter);
      disks.append(row);
    }
    content.append(disks);
  }

  private renderProcesses(metrics: ResourceMetrics): void {
    const processes = [...metrics.processes].sort((left, right) => {
      const a = this.sortKey === 'cpu' ? left.cpuPercent : left.memoryBytes;
      const b = this.sortKey === 'cpu' ? right.cpuPercent : right.memoryBytes;
      return (a - b) * this.sortDirection;
    }).slice(0, 8);
    setText(element<HTMLButtonElement>('sort-cpu'), `CPU ${this.sortKey === 'cpu' ? arrow(this.sortDirection) : ''}`);
    setText(element<HTMLButtonElement>('sort-memory'), `内存 ${this.sortKey === 'memory' ? arrow(this.sortDirection) : ''}`);
    const list = element<HTMLDivElement>('process-list');
    let maxMemory = 1;
    let maxCPU = 1;
    for (const process of metrics.processes) {
      maxMemory = Math.max(maxMemory, process.memoryBytes);
      maxCPU = Math.max(maxCPU, process.cpuPercent);
    }
    processes.forEach((process, index) => {
      let row = this.processRows[index];
      if (!row) {
        row = createProcessRow();
        this.processRows.push(row);
      }
      row.root.hidden = false;
      setText(row.memory, formatSize(process.memoryBytes));
      row.memory.style.background = barBackground(process.memoryBytes, maxMemory, '76, 141, 255');
      setText(row.cpu, process.cpuPercent.toFixed(1));
      row.cpu.style.background = barBackground(process.cpuPercent, maxCPU, '63, 185, 80');
      setText(row.command, process.command);
      row.command.title = `${process.pid} ${process.command}`;
      if (list.children[index] !== row.root) list.insertBefore(row.root, list.children[index] ?? null);
    });
    for (let index = processes.length; index < this.processRows.length; index += 1) {
      this.processRows[index]!.root.hidden = true;
    }
  }

  private renderNetwork(metrics: ResourceMetrics): void {
    if (!this.selectedInterface) this.selectedInterface = 'auto';
    const signature = metrics.interfaces.map((item) => item.name).join('\0');
    const select = element<HTMLSelectElement>('network-interface');
    if (signature !== this.interfaceSignature) {
      this.interfaceSignature = signature;
      select.replaceChildren(new Option('自动（活动网卡）', 'auto'));
      for (const iface of metrics.interfaces) {
        const option = document.createElement('option');
        option.value = iface.name;
        option.textContent = iface.name;
        select.append(option);
      }
      if (this.selectedInterface !== 'auto' && !metrics.interfaces.some((item) => item.name === this.selectedInterface)) {
        this.selectedInterface = 'auto';
      }
      select.value = this.selectedInterface;
    }
    const active = this.selectedInterface === 'auto'
      ? metrics.interfaces.reduce((best, item) => item.rxBytesPerSecond + item.txBytesPerSecond > (best?.rxBytesPerSecond ?? 0) + (best?.txBytesPerSecond ?? 0) ? item : best, metrics.interfaces[0])
      : metrics.interfaces.find((item) => item.name === this.selectedInterface) ?? metrics.interfaces[0];
    this.activeInterface = active?.name ?? '';
    if (active) {
      const rates = element<HTMLSpanElement>('network-rates');
      rates.textContent = `↑ ${compactRate(active.txBytesPerSecond)} ↓ ${compactRate(active.rxBytesPerSecond)}`;
      rates.title = `↑ ${formatRate(active.txBytesPerSecond)} ↓ ${formatRate(active.rxBytesPerSecond)} · ${active.name}`;
    }
    this.drawNetwork();
  }

  private renderDisks(metrics: ResourceMetrics): void {
    const list = element<HTMLDivElement>('disk-list');
    const activePaths = new Set(metrics.disks.map((disk) => disk.path));
    for (const [path, row] of this.diskRows) {
      if (activePaths.has(path)) continue;
      row.root.remove();
      this.diskRows.delete(path);
    }
    metrics.disks.forEach((disk, index) => {
      let row = this.diskRows.get(disk.path);
      if (!row) {
        row = createDiskRow(disk.path);
        this.diskRows.set(disk.path, row);
      }
      row.root.hidden = false;
      setText(row.size, `${formatSize(disk.used)} / ${formatSize(disk.total)}`);
      row.bar.style.width = `${Math.min(100, Math.max(0, disk.usedPercent))}%`;
      if (list.children[index] !== row.root) list.insertBefore(row.root, list.children[index] ?? null);
    });
  }

  private drawNetwork(): void {
    const target = this.sessionId ? this.historyFor(this.sessionId).network.get(this.activeInterface) ?? { rx: [], tx: [] } : { rx: [], tx: [] };
    const from = this.displayedNetwork;
    cancelAnimationFrame(this.networkAnimationFrame);
    if (from.rx.length === 0 && from.tx.length === 0) {
      this.displayedNetwork = target;
      drawNetworkBars(target);
      return;
    }
    drawNetworkBars(from);
    const startedAt = performance.now();
    const draw = (now: number) => {
      const progress = Math.min(1, (now - startedAt) / 320);
      const current = {
        rx: target.rx.map((value, index) => (from.rx[index] ?? 0) + (value - (from.rx[index] ?? 0)) * progress),
        tx: target.tx.map((value, index) => (from.tx[index] ?? 0) + (value - (from.tx[index] ?? 0)) * progress),
      };
      drawNetworkBars(current);
      if (progress < 1) {
        this.networkAnimationFrame = requestAnimationFrame(draw);
      } else {
        this.displayedNetwork = target;
      }
    };
    this.networkAnimationFrame = requestAnimationFrame(draw);
  }

  private drawPing(): void {
    const values = this.sessionId ? this.historyFor(this.sessionId).ping : [];
    drawChart('ping-chart', [{ values, color: '#79c0ff' }]);
  }

  private changeSort(key: 'cpu' | 'memory'): void {
    if (this.sortKey === key) this.sortDirection *= -1;
    else {
      this.sortKey = key;
      this.sortDirection = 1;
    }
    if (this.latest) {
      this.renderProcesses(this.latest);
      if (element<HTMLDialogElement>('metrics-dialog').open) this.renderDialog();
    }
  }

  private clearView(): void {
    this.latest = undefined;
    this.activeInterface = '';
    this.displayedNetwork = { rx: [], tx: [] };
    cancelAnimationFrame(this.networkAnimationFrame);
    element<HTMLDivElement>('system-uptime').textContent = '运行 —';
    element<HTMLDivElement>('system-load').textContent = '负载 —';
    element<HTMLSpanElement>('cpu-text').textContent = '—';
    element<HTMLSpanElement>('memory-text').textContent = '—';
    element<HTMLSpanElement>('swap-text').textContent = '—';
    setMeter('cpu-bar', 0);
    setMeter('memory-bar', 0);
    setMeter('swap-bar', 0);
    element<HTMLSpanElement>('network-rates').textContent = '↑ — ↓ —';
    element<HTMLSpanElement>('ping-text').textContent = '— ms';
    element<HTMLSelectElement>('network-interface').replaceChildren();
    for (const row of this.processRows) row.root.hidden = true;
    for (const row of this.diskRows.values()) row.root.remove();
    this.diskRows.clear();
    if (element<HTMLDialogElement>('metrics-dialog').open) this.renderDialog();
    this.drawNetwork();
    this.drawPing();
  }
}

function createProcessRow(): ProcessRow {
  const root = document.createElement('div');
  root.className = 'process-row';
  const memory = document.createElement('span');
  const cpu = document.createElement('span');
  const command = document.createElement('span');
  root.append(memory, cpu, command);
  return { root, memory, cpu, command };
}

function createDiskRow(path: string): DiskRow {
  const root = document.createElement('div');
  root.className = 'disk-row';
  const labels = document.createElement('div');
  const name = document.createElement('span');
  name.textContent = path;
  name.title = path;
  const size = document.createElement('span');
  labels.append(name, size);
  const meter = document.createElement('div');
  meter.className = 'meter';
  const bar = document.createElement('div');
  meter.append(bar);
  root.append(labels, meter);
  return { root, size, bar };
}

function setText(target: HTMLElement, value: string): void {
  if (target.textContent !== value) target.textContent = value;
}

function setMeter(id: string, value: number): void {
  const bar = element<HTMLDivElement>(id);
  const width = `${Math.min(100, Math.max(0, value))}%`;
  if (bar.style.width !== width) bar.style.width = width;
}

function compactRate(value: number): string {
  return formatRate(value).replace(' KB/s', 'K').replace(' MB/s', 'M').replace(' GB/s', 'G').replace(' B/s', 'B');
}

function arrow(direction: number): string {
  return direction > 0 ? '↑' : '↓';
}

function drawChart(id: string, series: { values: number[]; color: string }[]): void {
  const canvas = element<HTMLCanvasElement>(id);
  const width = canvas.clientWidth;
  const height = canvas.clientHeight;
  const ratio = window.devicePixelRatio || 1;
  if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
    canvas.width = Math.round(width * ratio);
    canvas.height = Math.round(height * ratio);
  }
  const context = canvas.getContext('2d');
  if (!context || width === 0) return;
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  context.clearRect(0, 0, width, height);
  context.strokeStyle = '#262a31';
  context.lineWidth = 1;
  for (let row = 1; row < 4; row += 1) {
    const y = (height / 4) * row;
    context.beginPath();
    context.moveTo(0, y);
    context.lineTo(width, y);
    context.stroke();
  }
  const maximum = Math.max(1, ...series.flatMap((item) => item.values));
  for (const item of series) {
    if (item.values.length === 0) continue;
    context.strokeStyle = item.color;
    context.lineWidth = 1.5;
    context.beginPath();
    item.values.forEach((value, index) => {
      const x = (index / Math.max(item.values.length - 1, 1)) * width;
      const y = height - (value / maximum) * (height - 6) - 3;
      if (index === 0) context.moveTo(x, y);
      else context.lineTo(x, y);
    });
    context.stroke();
  }
}

function addMetricCard(parent: HTMLElement, label: string, value: string, percent: number): void {
  const card = document.createElement('div');
  card.className = 'metric-card';
  const title = document.createElement('span');
  title.textContent = label;
  const strong = document.createElement('strong');
  strong.textContent = value;
  const meter = document.createElement('div');
  meter.className = 'meter';
  const bar = document.createElement('div');
  bar.style.width = `${Math.min(100, Math.max(0, percent))}%`;
  meter.append(bar);
  card.append(title, strong, meter);
  parent.append(card);
}

function textSpan(text: string): HTMLSpanElement {
  const span = document.createElement('span');
  span.textContent = text;
  return span;
}

function drawNetworkBars(history: NetworkHistory): void {
  const canvas = element<HTMLCanvasElement>('network-chart');
  const width = canvas.clientWidth;
  const height = canvas.clientHeight;
  const ratio = window.devicePixelRatio || 1;
  if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
    canvas.width = Math.round(width * ratio);
    canvas.height = Math.round(height * ratio);
  }
  const context = canvas.getContext('2d');
  if (!context || width === 0) return;
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  context.clearRect(0, 0, width, height);
  const maximum = Math.max(1, ...history.rx, ...history.tx);
  const centerY = Math.floor(height / 2) + 0.5;
  const plotHeight = Math.max(1, height / 2 - 14);

  context.strokeStyle = '#262a31';
  context.lineWidth = 1;
  context.setLineDash([2, 4]);
  for (const y of [centerY / 2, centerY + centerY / 2]) {
    context.beginPath();
    context.moveTo(0, y);
    context.lineTo(width, y);
    context.stroke();
  }
  context.setLineDash([]);
  context.strokeStyle = '#4b5563';
  context.beginPath();
  context.moveTo(0, centerY);
  context.lineTo(width, centerY);
  context.stroke();

  context.fillStyle = '#9aa3af';
  context.font = '10px -apple-system, sans-serif';
  context.fillText(`↑ ${formatRate(maximum)}`, 0, 10);
  context.fillText(`↓ ${formatRate(maximum)}`, 0, height - 4);
  const drawing = context;
  drawBars(history.tx, '#f85149', -1);
  drawBars(history.rx, '#3fb950', 1);

  function drawBars(values: number[], color: string, direction: -1 | 1): void {
    const barWidth = 3;
    const step = 5;
    const count = Math.min(values.length, Math.floor(width / step));
    const visibleValues = values.slice(-count);
    drawing.fillStyle = color;
    visibleValues.forEach((value, index) => {
      const barHeight = value > 0 ? Math.max(1, (value / maximum) * plotHeight) : 0;
      const x = width - (count - index) * step;
      const y = direction < 0 ? centerY - barHeight : centerY;
      drawing.fillRect(x, y, barWidth, barHeight);
    });
  }
}

function barBackground(value: number, maximum: number, rgb: string): string {
  const percent = Math.min(100, Math.max(0, (value / maximum) * 100));
  return `linear-gradient(to right, rgba(${rgb}, 0.24) ${percent}%, transparent ${percent}%)`;
}
