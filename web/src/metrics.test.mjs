import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

const source = await readFile(new URL('./metrics.ts', import.meta.url), 'utf8');
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
});
const utilsURL = new URL('./utils.ts', import.meta.url).href;
const moduleURL = `data:text/javascript;base64,${Buffer.from(outputText.replace("from './utils'", `from '${utilsURL}'`)).toString('base64')}`;
const { MetricsPanel } = await import(moduleURL);

function defineGlobal(t, name, value) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor);
    else delete globalThis[name];
  });
}

function fakeContext() {
  const calls = { clearRect: [], fillText: [], fillRect: [] };
  return {
    calls,
    fillStyle: '',
    setTransform() {},
    clearRect(...args) { calls.clearRect.push(args); },
    beginPath() {},
    moveTo() {},
    lineTo() {},
    stroke() {},
    setLineDash() {},
    fillRect(...args) { calls.fillRect.push({ args, color: this.fillStyle }); },
    fillText(...args) { calls.fillText.push(args); },
  };
}

function fakeCanvas(context) {
  let widthReads = 0;
  let heightReads = 0;
  return {
    canvas: {
      width: 0,
      height: 0,
      get clientWidth() { widthReads += 1; return 100; },
      get clientHeight() { heightReads += 1; return 80; },
      getContext() { return context; },
    },
    reads: () => ({ width: widthReads, height: heightReads }),
  };
}

function installDOM(t, elements) {
  defineGlobal(t, 'document', { getElementById: (id) => elements.get(id) });
  defineGlobal(t, 'window', { devicePixelRatio: 2 });
}

function sessionHistory(network = new Map(), ping = []) {
  return { network, ping, selectedInterface: 'auto' };
}

let nextTimestamp = 1000;

function networkPanel(target = { rx: [], tx: [], timestamps: [], maximum: 1 }) {
  if (!target.timestamps) target.timestamps = target.rx.map((_, index) => nextTimestamp - target.rx.length + index);
  const panel = Object.create(MetricsPanel.prototype);
  panel.sessionId = 'session';
  panel.activeInterface = 'eth0';
  panel.snapshots = new Map();
  panel.histories = new Map([['session', sessionHistory(new Map([['eth0', target]]))]]);
  return panel;
}

function sample(rx, tx = 0, name = 'eth0', timestamp = nextTimestamp++) {
  return {
    timestamp,
    interfaces: [{ name, rxBytesPerSecond: rx, txBytesPerSecond: tx }],
    pingMs: 0,
  };
}

function receiveBars(context) {
  return context.calls.fillRect.filter((call) => call.color === '#3fb950');
}

function heights(bars) {
  return bars.map((call) => call.args[3]);
}

function maximumLabel(context) {
  return context.calls.fillText.at(-2)[0];
}

test('each sample appends at the right and both directions share the same scale', (t) => {
  const context = fakeContext();
  const { canvas, reads } = fakeCanvas(context);
  installDOM(t, new Map([['network-chart', canvas]]));
  const history = { rx: [50, 100], tx: [25, 50], maximum: 100 };
  const panel = networkPanel(history);

  panel.drawNetwork();
  let bars = receiveBars(context);
  const sent = context.calls.fillRect.filter((call) => call.color === '#f85149');
  assert.deepEqual(bars.map((call) => call.args[0]), [90, 95]);
  assert.deepEqual(sent.map((call) => call.args[0]), [90, 95]);
  assert.deepEqual(heights(bars), [13, 26]);
  assert.deepEqual(heights(sent), [6.5, 13]);
  assert.equal(maximumLabel(context), '↑ 100 B/s');

  panel.recordHistory('session', sample(25));
  assert.deepEqual(history.rx, [50, 100, 25], 'existing samples are never interpolated or rewritten');
  assert.equal(history.maximum, 100);
  panel.drawNetwork();
  bars = receiveBars(context).slice(-3);
  assert.deepEqual(bars.map((call) => call.args[0]), [85, 90, 95]);
  assert.deepEqual(heights(bars), [13, 26, 6.5], 'a lower sample only shifts existing bars left');

  panel.recordHistory('session', sample(200));
  assert.deepEqual(history.rx, [50, 100, 25, 200]);
  assert.equal(history.maximum, 200);
  panel.drawNetwork();
  bars = receiveBars(context).slice(-4);
  assert.deepEqual(heights(bars), [6.5, 13, 3.25, 26], 'a new maximum scales every visible bar');
  assert.equal(maximumLabel(context), '↑ 200 B/s');

  const drawCount = context.calls.clearRect.length;
  panel.drawNetwork();
  assert.equal(context.calls.clearRect.length, drawCount, 'unchanged history is not redrawn');
  assert.deepEqual(reads(), { width: 1, height: 1 });
});

test('history keeps 40 samples and the shared maximum falls when either direction peak leaves', () => {
  const panel = networkPanel();
  panel.recordHistory('session', sample(1000, 2000));
  for (let value = 1; value <= 44; value += 1) panel.recordHistory('session', sample(value, value * 2));

  const history = panel.histories.get('session').network.get('eth0');
  assert.equal(history.rx.length, 40);
  assert.equal(history.tx.length, 40);
  assert.deepEqual(history.rx, Array.from({ length: 40 }, (_, index) => index + 5));
  assert.equal(history.rx.at(-1), 44);
  assert.equal(history.maximum, 88);

  panel.recordHistory('session', sample(1, 2));
  assert.deepEqual(history.rx.slice(0, 2), [6, 7]);
  assert.equal(history.rx.at(-1), 1);
  assert.equal(history.maximum, 88);
});

test('network maximums are isolated by session and interface and restored on switches', (t) => {
  const context = fakeContext();
  const { canvas } = fakeCanvas(context);
  installDOM(t, new Map([['network-chart', canvas]]));
  const panel = networkPanel({ rx: [50], tx: [0], maximum: 100 });
  panel.histories.get('session').network.set('eth1', { rx: [50], tx: [0], maximum: 400 });
  panel.histories.set('other', sessionHistory(new Map([
    ['eth0', { rx: [50], tx: [0], maximum: 200 }],
  ])));

  panel.drawNetwork();
  assert.equal(maximumLabel(context), '↑ 100 B/s');
  assert.equal(receiveBars(context).at(-1).args[3], 13);

  panel.activeInterface = 'eth1';
  panel.drawNetwork();
  assert.equal(maximumLabel(context), '↑ 400 B/s');
  assert.equal(receiveBars(context).at(-1).args[3], 3.25);

  panel.sessionId = 'other';
  panel.activeInterface = 'eth0';
  panel.drawNetwork();
  assert.equal(maximumLabel(context), '↑ 200 B/s');
  assert.equal(receiveBars(context).at(-1).args[3], 6.5);

  panel.sessionId = 'session';
  panel.drawNetwork();
  assert.equal(maximumLabel(context), '↑ 100 B/s');
  assert.equal(receiveBars(context).at(-1).args[3], 13);
  assert.equal(context.calls.clearRect.length, 4, 'equal samples with different maximums must still redraw');
});

test('manual interface selection immediately renders the selected rates and history', (t) => {
  const context = fakeContext();
  const { canvas } = fakeCanvas(context);
  const rates = { textContent: '', title: '' };
  installDOM(t, new Map([
    ['network-chart', canvas],
    ['network-rates', rates],
    ['network-interface', {}],
  ]));
  const panel = networkPanel({ rx: [50], tx: [0], maximum: 100 });
  panel.histories.get('session').network.set('eth1', { rx: [50], tx: [0], maximum: 400 });
  panel.interfaceSignature = 'eth0\0eth1';
  panel.latest = {
    interfaces: [
      { name: 'eth0', rxBytesPerSecond: 50, txBytesPerSecond: 0 },
      { name: 'eth1', rxBytesPerSecond: 50, txBytesPerSecond: 400 },
    ],
  };

  panel.selectInterface('eth1');

  assert.equal(panel.activeInterface, 'eth1');
  assert.equal(rates.textContent, '↑ 400B ↓ 50B');
  assert.equal(maximumLabel(context), '↑ 400 B/s');
});

test('automatic selection keeps the current interface instead of following transient peaks', (t) => {
  const context = fakeContext();
  const { canvas } = fakeCanvas(context);
  installDOM(t, new Map([
    ['network-chart', canvas],
    ['network-rates', { textContent: '', title: '' }],
    ['network-interface', {}],
  ]));
  const panel = networkPanel({ rx: [1], tx: [1], maximum: 1 });
  panel.histories.get('session').network.set('eth1', { rx: [100], tx: [100], maximum: 100 });
  panel.selectedInterface = 'auto';
  panel.interfaceSignature = 'eth0\0eth1';
  const metrics = {
    interfaces: [
      { name: 'eth0', rxBytesPerSecond: 1, txBytesPerSecond: 1 },
      { name: 'eth1', rxBytesPerSecond: 100, txBytesPerSecond: 100 },
    ],
  };

  panel.renderNetwork(metrics);
  assert.equal(panel.activeInterface, 'eth0');

  panel.activeInterface = '';
  panel.renderNetwork(metrics);
  assert.equal(panel.activeInterface, 'eth1');
});

test('dedicated network samples update bars without duplicating resource frames', () => {
  const panel = networkPanel();
  const resource = { timestamp: 2000, interfaces: [{ name: 'eth0', rxBytesPerSecond: 999, txBytesPerSecond: 999 }], pingMs: 12 };

  panel.receive('session', resource);
  assert.deepEqual(panel.histories.get('session').network.get('eth0').rx, []);
  assert.deepEqual(panel.histories.get('session').ping, [12]);

  panel.receiveNetwork('session', { timestamp: 2000, interfaces: [{ name: 'eth0', rxBytesPerSecond: 100, txBytesPerSecond: 50 }] });
  panel.receiveNetwork('session', { timestamp: 2001, interfaces: [{ name: 'eth0', rxBytesPerSecond: 0, txBytesPerSecond: 0 }] });
  const history = panel.histories.get('session').network.get('eth0');
  assert.deepEqual(history.timestamps, [2000, 2001]);
  assert.deepEqual(history.rx, [100, 0]);
  assert.deepEqual(history.tx, [50, 0]);
  assert.deepEqual(panel.histories.get('session').ping, [12]);
});

test('timestamped history fills gaps and replaces same-second and late samples', () => {
  const panel = networkPanel();
  panel.recordHistory('session', sample(100, 50, 'eth0', 2000));
  panel.recordHistory('session', sample(25, 5, 'eth0', 2003));
  panel.recordHistory('session', sample(75, 60, 'eth0', 2001));
  panel.recordHistory('session', sample(80, 70, 'eth0', 2003));

  const history = panel.histories.get('session').network.get('eth0');
  assert.deepEqual(history.timestamps, [2000, 2001, 2002, 2003]);
  assert.deepEqual(history.rx, [100, 75, 0, 80]);
  assert.deepEqual(history.tx, [50, 60, 0, 70]);
  assert.equal(history.maximum, 100);
});

test('network time advances with zero samples and expires peaks after 40 seconds', (t) => {
  const panel = networkPanel();
  panel.recordHistory('session', sample(1000, 500, 'eth0', 2000));
  defineGlobal(t, 'Date', { now: () => 2040 * 1000 });

  panel.advanceNetworkTime();

  const history = panel.histories.get('session').network.get('eth0');
  assert.equal(history.timestamps.length, 40);
  assert.equal(history.timestamps[0], 2001);
  assert.equal(history.timestamps.at(-1), 2040);
  assert.ok(history.rx.slice(1).every((value) => value === 0));
  assert.ok(history.tx.slice(1).every((value) => value === 0));
  assert.equal(history.maximum, 1);
});

test('network chart shows the time range and exact sample timestamp on hover', (t) => {
  const context = fakeContext();
  const { canvas } = fakeCanvas(context);
  canvas.title = '';
  canvas.getBoundingClientRect = () => ({ left: 0, width: 100 });
  const start = { textContent: '' };
  const end = { textContent: '' };
  installDOM(t, new Map([
    ['network-chart', canvas],
    ['network-time-start', start],
    ['network-time-end', end],
  ]));
  const panel = networkPanel({ rx: [50], tx: [25], timestamps: [2000], maximum: 100 });

  panel.drawNetwork();
  panel.showNetworkTooltip({ currentTarget: canvas, clientX: 95 });

  const clock = new Date(2000 * 1000).toLocaleTimeString('zh-CN', { hour12: false });
  assert.equal(start.textContent, clock);
  assert.equal(end.textContent, clock);
  assert.equal(canvas.title, `${clock}\n↑ 25 B/s ↓ 50 B/s`);
});

test('ping chart skips unchanged rendering without rereading layout', (t) => {
  const context = fakeContext();
  const { canvas, reads } = fakeCanvas(context);
  installDOM(t, new Map([['ping-chart', canvas]]));
  const panel = networkPanel();
  panel.histories.get('session').ping = [10, 20];

  panel.drawPing();
  panel.drawPing();
  assert.equal(context.calls.clearRect.length, 1);
  assert.deepEqual(reads(), { width: 1, height: 1 });

  panel.histories.get('session').ping.push(30);
  panel.drawPing();
  assert.equal(context.calls.clearRect.length, 2);
  assert.deepEqual(reads(), { width: 1, height: 1 });
});

test('clearView clears the current charts without deleting session history', (t) => {
  const networkContext = fakeContext();
  const pingContext = fakeContext();
  const network = fakeCanvas(networkContext);
  const ping = fakeCanvas(pingContext);
  const elements = new Map([
    ['network-chart', network.canvas],
    ['ping-chart', ping.canvas],
    ['metrics-dialog', { open: false }],
    ['network-interface', { replaceChildren() {} }],
  ]);
  for (const id of ['system-uptime', 'system-load', 'cpu-text', 'memory-text', 'swap-text', 'network-rates', 'ping-text']) {
    elements.set(id, { textContent: '' });
  }
  for (const id of ['cpu-bar', 'memory-bar', 'swap-bar']) elements.set(id, { style: { width: '' } });
  installDOM(t, elements);
  const history = { rx: [100, 200], tx: [0, 0], maximum: 200 };
  const panel = networkPanel(history);
  panel.processRows = [];
  panel.diskRows = new Map();
  panel.drawNetwork();
  const barCount = networkContext.calls.fillRect.length;

  panel.sessionId = undefined;
  panel.clearView();

  assert.equal(panel.activeInterface, '');
  assert.equal(networkContext.calls.clearRect.length, 2);
  assert.equal(networkContext.calls.fillRect.length, barCount, 'the cleared chart contains no bars');
  assert.equal(maximumLabel(networkContext), '↑ 1 B/s');
  assert.equal(pingContext.calls.clearRect.length, 1);
  assert.equal(elements.get('network-rates').textContent, '↑ — ↓ —');
  assert.equal(elements.get('ping-text').textContent, '— ms');
  assert.strictEqual(panel.histories.get('session').network.get('eth0'), history);
  assert.equal(history.maximum, 200);
});
