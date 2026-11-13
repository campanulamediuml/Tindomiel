import type { Api } from './api';
import type { DisplayStatus, NetworkMetrics, ResourceMetrics } from './types';
import { element, formatDuration, formatRate, formatSize } from './utils';

interface NetworkHistory {
  rx: number[];
  tx: number[];
  timestamps: number[];
  maximum: number;
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

interface CanvasGeometry {
  width: number;
  height: number;
}

const canvasGeometries = new WeakMap<HTMLCanvasElement, CanvasGeometry>();
const canvasRenderSignatures = new WeakMap<HTMLCanvasElement, string>();

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
  private sessionVersion = 0;
  private readonly processRows: ProcessRow[] = [];
  private readonly diskRows = new Map<string, DiskRow>();
  private readonly tooltip: HTMLDivElement;

  constructor(private readonly api: Api, onError: (message: string) => void) {
    this.onError = onError;
    this.tooltip = document.createElement('div');
    this.tooltip.className = 'chart-tooltip';
    this.tooltip.hidden = true;
    document.body.append(this.tooltip);
    element<HTMLButtonElement>('sort-cpu').addEventListener('click', () => this.changeSort('cpu'));
    element<HTMLButtonElement>('sort-memory').addEventListener('click', () => this.changeSort('memory'));
    element<HTMLSelectElement>('network-interface').addEventListener('change', (event) => {
      this.selectInterface((event.currentTarget as HTMLSelectElement).value);
    });
    element<HTMLButtonElement>('copy-ip').addEventListener('click', () => {
      const host = element<HTMLElement>('target-ip').dataset.host;
      if (host) void navigator.clipboard.writeText(host).then(() => this.onError(`已复制 ${host}`));
    });
    element<HTMLButtonElement>('system-info-button').addEventListener('click', () => {
      this.renderDialog();
      element<HTMLDialogElement>('metrics-dialog').showModal();
    });
    new ResizeObserver(() => this.redrawCharts()).observe(element<HTMLElement>('metrics-panel'));
    document.addEventListener('visibilitychange', () => this.redrawCharts());
    window.addEventListener('focus', () => this.redrawCharts());
    window.addEventListener('pageshow', () => this.redrawCharts());
    const networkCanvas = element<HTMLCanvasElement>('network-chart');
    networkCanvas.addEventListener('mousemove', (event) => this.showNetworkTooltip(event));
    networkCanvas.addEventListener('mouseleave', () => this.hideTooltip());
    const pingCanvas = element<HTMLCanvasElement>('ping-chart');
    pingCanvas.addEventListener('mousemove', (event) => this.showPingTooltip(event));
    pingCanvas.addEventListener('mouseleave', () => this.hideTooltip());
    window.setInterval(() => this.advanceNetworkTime(), 1000);
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
    this.recordPing(sessionId, metrics);
    if (this.sessionId === sessionId && this.status === 'ready') this.render(metrics);
  }

  receiveNetwork(sessionId: string, metrics: NetworkMetrics): void {
    const cached = this.snapshots.get(sessionId);
    if (cached) cached.interfaces = metrics.interfaces;
    this.recordHistory(sessionId, metrics);
    if (this.sessionId !== sessionId || this.status !== 'ready') return;
    if (this.latest) this.latest.interfaces = metrics.interfaces;
    this.renderNetwork(metrics);
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

  private recordHistory(sessionId: string, metrics: NetworkMetrics): void {
    const history = this.historyFor(sessionId);
    const timestamp = metrics.timestamp ?? Math.floor(Date.now() / 1000);
    const names = new Set([...history.network.keys(), ...metrics.interfaces.map((iface) => iface.name)]);
    for (const name of names) {
      const iface = metrics.interfaces.find((item) => item.name === name);
      const network = history.network.get(name) ?? { rx: [], tx: [], timestamps: [], maximum: 1 };
      appendNetworkSample(network, timestamp, iface?.rxBytesPerSecond ?? 0, iface?.txBytesPerSecond ?? 0);
      history.network.set(name, network);
    }
  }

  private recordPing(sessionId: string, metrics: ResourceMetrics): void {
    const history = this.historyFor(sessionId).ping;
    history.push(metrics.pingMs);
    if (history.length > 40) history.shift();
  }

  private advanceNetworkTime(): void {
    const timestamp = Math.floor(Date.now() / 1000);
    for (const history of this.histories.values()) {
      for (const network of history.network.values()) {
        if (network.timestamps.length > 0) appendNetworkSample(network, timestamp, 0, 0);
      }
    }
    if (this.sessionId && this.status === 'ready') this.drawNetwork();
  }

  private async loadCached(): Promise<void> {
    const sessionId = this.sessionId;
    const version = this.sessionVersion;
    if (!sessionId) return;
    try {
      const metrics = await this.api.metrics(sessionId);
      this.snapshots.set(sessionId, metrics);
      this.recordPing(sessionId, metrics);
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
      if (row.root.hidden) row.root.hidden = false;
      setText(row.memory, formatSize(process.memoryBytes));
      setBackground(row.memory, barBackground(process.memoryBytes, maxMemory, '76, 141, 255'));
      setText(row.cpu, process.cpuPercent.toFixed(1));
      setBackground(row.cpu, barBackground(process.cpuPercent, maxCPU, '63, 185, 80'));
      setText(row.command, process.command);
      const title = `${process.pid} ${process.command}`;
      if (row.command.title !== title) row.command.title = title;
      if (list.children[index] !== row.root) list.insertBefore(row.root, list.children[index] ?? null);
    });
    for (let index = processes.length; index < this.processRows.length; index += 1) {
      const row = this.processRows[index]!;
      if (!row.root.hidden) row.root.hidden = true;
    }
  }

  private selectInterface(value: string): void {
    this.selectedInterface = value;
    this.activeInterface = value === 'auto' ? '' : value;
    if (this.latest) this.renderNetwork(this.latest);
    else this.drawNetwork();
  }

  private renderNetwork(metrics: NetworkMetrics): void {
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
    const current = metrics.interfaces.find((item) => item.name === this.activeInterface);
    const active = this.selectedInterface === 'auto'
      ? current ?? metrics.interfaces.reduce((best, item) => item.rxBytesPerSecond + item.txBytesPerSecond > (best?.rxBytesPerSecond ?? 0) + (best?.txBytesPerSecond ?? 0) ? item : best, metrics.interfaces[0])
      : metrics.interfaces.find((item) => item.name === this.selectedInterface) ?? metrics.interfaces[0];
    this.activeInterface = active?.name ?? '';
    const rates = element<HTMLSpanElement>('network-rates');
    if (active) {
      setText(rates, `↑ ${compactRate(active.txBytesPerSecond)} ↓ ${compactRate(active.rxBytesPerSecond)}`);
      const title = `↑ ${formatRate(active.txBytesPerSecond)} ↓ ${formatRate(active.rxBytesPerSecond)} · ${active.name}`;
      if (rates.title !== title) rates.title = title;
    } else {
      setText(rates, '↑ — ↓ —');
      rates.title = '';
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
      if (row.root.hidden) row.root.hidden = false;
      setText(row.size, `${formatSize(disk.used)} / ${formatSize(disk.total)}`);
      setWidth(row.bar, disk.usedPercent);
      if (list.children[index] !== row.root) list.insertBefore(row.root, list.children[index] ?? null);
    });
  }

  private showNetworkTooltip(event: MouseEvent): void {
    const canvas = event.currentTarget as HTMLCanvasElement;
    const history = this.sessionId ? this.historyFor(this.sessionId).network.get(this.activeInterface) : undefined;
    if (!history) {
      this.hideTooltip();
      return;
    }
    history.timestamps ??= [];
    const bounds = canvas.getBoundingClientRect();
    const count = Math.min(history.timestamps.length, Math.floor(bounds.width / 5));
    const fromRight = Math.max(0, Math.ceil((bounds.width - (event.clientX - bounds.left)) / 5) - 1);
    const visibleIndex = count - fromRight - 1;
    if (visibleIndex < 0 || visibleIndex >= count) {
      this.hideTooltip();
      return;
    }
    const index = history.timestamps.length - count + visibleIndex;
    this.showTooltip(event, `${formatClock(history.timestamps[index]!)}\n↑ ${formatRate(history.tx[index] ?? 0)}\n↓ ${formatRate(history.rx[index] ?? 0)}`);
  }

  private showPingTooltip(event: MouseEvent): void {
    const canvas = event.currentTarget as HTMLCanvasElement;
    const values = this.sessionId ? this.historyFor(this.sessionId).ping : [];
    if (values.length === 0) {
      this.hideTooltip();
      return;
    }
    const bounds = canvas.getBoundingClientRect();
    const position = bounds.width > 0 ? (event.clientX - bounds.left) / bounds.width : 0;
    const index = Math.round(Math.min(1, Math.max(0, position)) * (values.length - 1));
    const value = values[index] ?? 0;
    this.showTooltip(event, `Ping\n${value > 0 ? `${value.toFixed(1)} ms` : '— ms'}`);
  }

  private showTooltip(event: MouseEvent, text: string): void {
    this.tooltip.textContent = text;
    this.tooltip.hidden = false;
    const maxLeft = Math.max(8, window.innerWidth - this.tooltip.offsetWidth - 8);
    const maxTop = Math.max(8, window.innerHeight - this.tooltip.offsetHeight - 8);
    this.tooltip.style.left = `${Math.max(8, Math.min(event.clientX + 12, maxLeft))}px`;
    this.tooltip.style.top = `${Math.max(8, Math.min(event.clientY + 12, maxTop))}px`;
  }

  private hideTooltip(): void {
    this.tooltip.hidden = true;
    this.tooltip.textContent = '';
  }

  private redrawCharts(): void {
    if (document.hidden) return;
    this.hideTooltip();
    const network = element<HTMLCanvasElement>('network-chart');
    const ping = element<HTMLCanvasElement>('ping-chart');
    canvasGeometries.delete(network);
    canvasGeometries.delete(ping);
    canvasRenderSignatures.delete(network);
    canvasRenderSignatures.delete(ping);
    this.drawNetwork();
    this.drawPing();
  }

  private drawNetwork(): void {
    const history = this.sessionId ? this.historyFor(this.sessionId).network.get(this.activeInterface) : undefined;
    drawNetworkBars(history ?? { rx: [], tx: [], timestamps: [], maximum: 1 });
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
    this.hideTooltip();
    setText(element<HTMLDivElement>('system-uptime'), '运行 —');
    setText(element<HTMLDivElement>('system-load'), '负载 —');
    setText(element<HTMLSpanElement>('cpu-text'), '—');
    setText(element<HTMLSpanElement>('memory-text'), '—');
    setText(element<HTMLSpanElement>('swap-text'), '—');
    setMeter('cpu-bar', 0);
    setMeter('memory-bar', 0);
    setMeter('swap-bar', 0);
    setText(element<HTMLSpanElement>('network-rates'), '↑ — ↓ —');
    setText(element<HTMLSpanElement>('ping-text'), '— ms');
    element<HTMLSelectElement>('network-interface').replaceChildren();
    for (const row of this.processRows) {
      if (!row.root.hidden) row.root.hidden = true;
    }
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
  setWidth(element<HTMLDivElement>(id), value);
}

function setWidth(target: HTMLElement, value: number): void {
  const width = `${Math.min(100, Math.max(0, value))}%`;
  if (target.style.width !== width) target.style.width = width;
}

function setBackground(target: HTMLElement, value: string): void {
  if (target.style.background !== value) target.style.background = value;
}

function compactRate(value: number): string {
  return formatRate(value).replace(' KB/s', 'K').replace(' MB/s', 'M').replace(' GB/s', 'G').replace(' B/s', 'B');
}

function arrow(direction: number): string {
  return direction > 0 ? '↑' : '↓';
}

function geometryFor(canvas: HTMLCanvasElement): CanvasGeometry {
  const cached = canvasGeometries.get(canvas);
  if (cached) return cached;
  const geometry = { width: canvas.clientWidth, height: canvas.clientHeight };
  canvasGeometries.set(canvas, geometry);
  return geometry;
}

function drawChart(id: string, series: { values: number[]; color: string }[]): void {
  const canvas = element<HTMLCanvasElement>(id);
  const { width, height } = geometryFor(canvas);
  const ratio = window.devicePixelRatio || 1;
  const signature = JSON.stringify([width, height, ratio, series.map((item) => [item.color, item.values])]);
  if (canvasRenderSignatures.get(canvas) === signature) return;
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
  canvasRenderSignatures.set(canvas, signature);
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

function appendNetworkSample(history: NetworkHistory, timestamp: number, rx: number, tx: number): void {
  history.timestamps ??= [];
  const second = Math.floor(timestamp);
  if (history.timestamps.length === 0 && history.rx.length > 0) {
    const first = second - history.rx.length;
    history.timestamps = history.rx.map((_, index) => first + index);
  }
  const existing = history.timestamps.indexOf(second);
  if (existing >= 0) {
    history.rx[existing] = rx;
    history.tx[existing] = tx;
  } else if (history.timestamps.length === 0) {
    history.timestamps.push(second);
    history.rx.push(rx);
    history.tx.push(tx);
  } else {
    const last = history.timestamps.at(-1)!;
    if (second <= last) return;
    const firstMissing = Math.max(last + 1, second - 39);
    for (let missing = firstMissing; missing < second; missing += 1) {
      history.timestamps.push(missing);
      history.rx.push(0);
      history.tx.push(0);
    }
    history.timestamps.push(second);
    history.rx.push(rx);
    history.tx.push(tx);
  }
  const excess = history.rx.length - 40;
  if (excess > 0) {
    history.rx.splice(0, excess);
    history.tx.splice(0, excess);
    history.timestamps.splice(0, excess);
  }
  history.maximum = Math.max(1, ...history.rx, ...history.tx);
}

function drawNetworkBars(history: NetworkHistory): void {
  history.timestamps ??= [];
  const canvas = element<HTMLCanvasElement>('network-chart');
  const { width, height } = geometryFor(canvas);
  const ratio = window.devicePixelRatio || 1;
  const signature = JSON.stringify([width, height, ratio, history.maximum, history.rx, history.tx, history.timestamps]);
  if (canvasRenderSignatures.get(canvas) === signature) return;
  if (canvas.width !== Math.round(width * ratio) || canvas.height !== Math.round(height * ratio)) {
    canvas.width = Math.round(width * ratio);
    canvas.height = Math.round(height * ratio);
  }
  const context = canvas.getContext('2d');
  if (!context || width === 0) return;
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  context.clearRect(0, 0, width, height);
  const visibleCount = Math.min(history.timestamps.length, Math.floor(width / 5));
  const visibleTimestamps = history.timestamps.slice(-visibleCount);
  const startLabel = document.getElementById('network-time-start');
  const endLabel = document.getElementById('network-time-end');
  if (startLabel) setText(startLabel, visibleTimestamps.length ? formatClock(visibleTimestamps[0]!) : '—');
  if (endLabel) setText(endLabel, visibleTimestamps.length ? formatClock(visibleTimestamps.at(-1)!) : '—');
  const maximum = history.maximum;
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
  canvasRenderSignatures.set(canvas, signature);
}

function formatClock(timestamp: number): string {
  return new Date(timestamp * 1000).toLocaleTimeString('zh-CN', { hour12: false });
}

function barBackground(value: number, maximum: number, rgb: string): string {
  const percent = Math.min(100, Math.max(0, (value / maximum) * 100));
  return `linear-gradient(to right, rgba(${rgb}, 0.24) ${percent}%, transparent ${percent}%)`;
}
