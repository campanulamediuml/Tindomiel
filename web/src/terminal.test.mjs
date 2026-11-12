import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { registerHooks } from 'node:module';
import ts from 'typescript';
import { WebglAddon } from '@xterm/addon-webgl';

const terminalUrl = new URL('./terminal.ts', import.meta.url).href;
const xtermUrl = 'test:@xterm/xterm';
const fitAddonUrl = 'test:@xterm/addon-fit';
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === '@xterm/xterm') return { url: xtermUrl, shortCircuit: true };
    if (specifier === '@xterm/addon-fit') return { url: fitAddonUrl, shortCircuit: true };
    if (context.parentURL === terminalUrl && (specifier === './shortcuts' || specifier === './terminal-themes')) {
      return { url: new URL(`${specifier}.ts`, terminalUrl).href, shortCircuit: true };
    }
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (url === xtermUrl) return { format: 'module', source: 'export class Terminal {}', shortCircuit: true };
    if (url === fitAddonUrl) return { format: 'module', source: 'export class FitAddon {}', shortCircuit: true };
    if (url !== terminalUrl) return nextLoad(url, context);
    return {
      format: 'module',
      source: ts.transpileModule(readFileSync(fileURLToPath(url), 'utf8'), {
        compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
      }).outputText,
      shortCircuit: true,
    };
  },
});
const { TerminalView } = await import('./terminal.ts');

function setup(t) {
  const activations = [];
  const disposals = [];
  const domRenderers = [];
  const rendererChanges = [];
  const rendererResizes = [];
  const output = [];
  const errors = [];
  let activationFailures = 0;
  let disposalFailures = 0;
  let terminalDisposals = 0;
  let removals = 0;
  let currentRenderer;
  const dimensions = {
    css: { canvas: { width: 640, height: 384 }, cell: { width: 8, height: 16 } },
    device: {
      canvas: { width: 1280, height: 768 },
      cell: { width: 16, height: 32 },
      char: { width: 16, height: 32, top: 0, left: 0 },
    },
  };

  t.mock.method(WebglAddon.prototype, 'activate', function () {
    activations.push(this);
    this._terminal = terminal;
    if (activationFailures > 0) {
      activationFailures -= 1;
      throw new Error('WebGL activation failed');
    }
    terminal._core._renderService.setRenderer({ type: 'webgl', addon: this, dimensions });
  });
  t.mock.method(WebglAddon.prototype, 'dispose', function () {
    disposals.push(this);
    if (disposalFailures > 0) {
      disposalFailures -= 1;
      throw new TypeError("Cannot read properties of undefined (reading '_isDisposed')");
    }
    const core = this._terminal?._core;
    if (core) {
      core._renderService.setRenderer(core._createRenderer());
      core._renderService.handleResize(this._terminal.cols, this._terminal.rows);
    }
  });

  const terminal = {
    options: {},
    cols: 80,
    rows: 24,
    _core: {
      _renderService: {
        get dimensions() {
          return currentRenderer.dimensions;
        },
        setRenderer(renderer) {
          currentRenderer?.dispose?.();
          currentRenderer = renderer;
          rendererChanges.push(renderer);
          renderer.onRequestRedraw?.(() => undefined);
        },
        handleResize(cols, rows) {
          currentRenderer?.handleResize?.(cols, rows);
        },
        handleCursorMove() {
          currentRenderer?.handleCursorMove?.();
        },
      },
      _createRenderer() {
        const renderer = {
          type: 'dom',
          dimensions,
          handleResize(cols, rows) {
            rendererResizes.push([cols, rows]);
          },
        };
        domRenderers.push(renderer);
        return renderer;
      },
    },
    loadAddon(addon) {
      addon.activate(terminal);
    },
    write(data) {
      output.push(data);
      void terminal._core._renderService.dimensions.css.canvas.width;
      terminal._core._renderService.handleCursorMove();
    },
    scrollToBottom() {},
    focus() {},
    dispose() {
      terminalDisposals += 1;
    },
  };
  const view = Object.create(TerminalView.prototype);
  Object.assign(view, {
    element: {
      hidden: false,
      remove() {
        removals += 1;
      },
    },
    terminal,
    disposed: false,
    resizeFrame: 0,
    resizeObserver: { disconnect() {} },
    webgl: undefined,
    webglReleaseTimer: undefined,
    onError(message) {
      errors.push(message);
    },
    scheduleFit() {},
  });
  view.restoreWebgl();

  return {
    view,
    activations,
    disposals,
    domRenderers,
    rendererChanges,
    rendererResizes,
    dimensions,
    output,
    errors,
    failNextActivation() {
      activationFailures = 1;
    },
    failNextDisposal() {
      disposalFailures = 1;
    },
    get terminalDisposals() {
      return terminalDisposals;
    },
    get removals() {
      return removals;
    },
  };
}

function useFakeTimers(t) {
  t.mock.timers.enable({ apis: ['setTimeout'] });
}

test('releases WebGL once after 30 seconds hidden without creating a DOM renderer', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals, domRenderers, rendererChanges, dimensions } = setup(t);
  const terminal = view.terminal;

  view.deactivate();
  t.mock.timers.tick(20_000);
  view.deactivate();
  t.mock.timers.tick(9_999);
  assert.equal(view.element.hidden, true);
  assert.equal(disposals.length, 0);

  t.mock.timers.tick(1);
  assert.deepEqual(disposals, activations);
  assert.equal(domRenderers.length, 0);
  assert.equal(rendererChanges.at(-1).dimensions, dimensions);
  assert.equal(view.terminal, terminal);

  view.activate();
  view.activate();
  assert.equal(view.element.hidden, false);
  assert.equal(activations.length, 2);
  assert.equal(disposals.length, 1);
  assert.equal(rendererChanges.at(-1).type, 'webgl');
  assert.equal(view.terminal, terminal);
});

test('inactive renderer implements the complete renderer interface as no-ops', (t) => {
  useFakeTimers(t);
  const { view, domRenderers, rendererChanges, dimensions } = setup(t);

  view.deactivate();
  t.mock.timers.tick(30_000);
  const renderer = rendererChanges.at(-1);
  const methods = [
    'dispose',
    'handleDevicePixelRatioChange',
    'handleResize',
    'handleCharSizeChanged',
    'handleBlur',
    'handleFocus',
    'handleSelectionChanged',
    'handleCursorMove',
    'clear',
    'renderRows',
    'clearTextureAtlas',
  ];

  assert.equal(renderer.dimensions, dimensions);
  assert.equal(typeof renderer.onRequestRedraw, 'function');
  for (const method of methods) assert.equal(typeof renderer[method], 'function', method);
  const subscription = renderer.onRequestRedraw(() => {
    throw new Error('inactive renderer must not request redraws');
  });
  assert.doesNotThrow(() => {
    subscription.dispose();
    renderer.handleDevicePixelRatioChange();
    renderer.handleResize(120, 40);
    renderer.handleCharSizeChanged();
    renderer.handleBlur();
    renderer.handleFocus();
    renderer.handleSelectionChanged([0, 0], [1, 1], false);
    renderer.handleCursorMove();
    renderer.clear();
    renderer.renderRows(0, 23);
    renderer.clearTextureAtlas();
    renderer.dispose();
  });
  assert.equal(renderer.dimensions, dimensions);
  assert.equal(domRenderers.length, 0);
});

test('cancels release during rapid tab switches', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals } = setup(t);

  for (let index = 0; index < 3; index += 1) {
    view.deactivate();
    t.mock.timers.tick(29_999);
    view.activate();
    t.mock.timers.tick(30_000);
  }

  assert.equal(activations.length, 1);
  assert.equal(disposals.length, 0);
});

test('continues receiving hidden output with an inactive renderer and restores on activation', (t) => {
  useFakeTimers(t);
  const { view, activations, domRenderers, rendererChanges, dimensions, output } = setup(t);

  view.deactivate();
  t.mock.timers.tick(30_000);
  assert.doesNotThrow(() => view.terminal.write('hidden htop update'));

  assert.deepEqual(output, ['hidden htop update']);
  assert.equal(domRenderers.length, 0);
  assert.equal(rendererChanges.at(-1).dimensions, dimensions);

  view.activate();
  assert.doesNotThrow(() => view.terminal.write('visible update'));
  assert.equal(activations.length, 2);
  assert.equal(rendererChanges.at(-1).type, 'webgl');
  assert.deepEqual(output, ['hidden htop update', 'visible update']);
});

test('installs a DOM renderer when visible WebGL disposal throws and restores on activation', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals, domRenderers, rendererChanges, rendererResizes, errors, failNextDisposal } = setup(t);
  const terminal = view.terminal;
  const lostAddon = activations[0];

  failNextDisposal();
  assert.doesNotThrow(() => lostAddon._onContextLoss.fire());

  assert.equal(view.element.hidden, false);
  assert.equal(disposals.length, 1);
  assert.equal(domRenderers.length, 1);
  assert.equal(rendererChanges.at(-1).type, 'dom');
  assert.deepEqual(rendererResizes, [[80, 24]]);
  assert.equal(view.webgl, undefined);
  assert.equal(view.terminal, terminal);
  assert.deepEqual(errors, []);

  view.activate();
  assert.equal(activations.length, 2);
  assert.equal(view.webgl, activations[1]);
  assert.equal(rendererChanges.at(-1).type, 'webgl');
  assert.equal(domRenderers.length, 1);
});

test('handles context loss and ignores stale context loss events', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals } = setup(t);
  const lostAddon = activations[0];

  view.deactivate();
  lostAddon._onContextLoss.fire();
  lostAddon._onContextLoss.fire();
  assert.equal(disposals.length, 1);

  t.mock.timers.tick(30_000);
  assert.equal(disposals.length, 1);
  view.activate();
  assert.equal(activations.length, 2);

  lostAddon._onContextLoss.fire();
  assert.equal(disposals.length, 1);
  assert.equal(view.webgl, activations[1]);
});

test('falls back once and reports an error when visible WebGL restoration fails', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals, domRenderers, rendererChanges, rendererResizes, errors, failNextActivation } = setup(t);

  view.deactivate();
  t.mock.timers.tick(30_000);
  failNextActivation();
  assert.doesNotThrow(() => view.activate());

  assert.equal(view.element.hidden, false);
  assert.equal(activations.length, 2);
  assert.equal(disposals.length, 2);
  assert.equal(view.webgl, undefined);
  assert.equal(domRenderers.length, 1);
  assert.equal(rendererChanges.at(-1).type, 'dom');
  assert.deepEqual(rendererResizes, [[80, 24]]);
  assert.deepEqual(errors, ['WebGL 不可用，已使用 xterm 普通渲染模式']);

  view.activate();
  assert.equal(activations.length, 3);
  assert.equal(view.webgl, activations[2]);
  assert.equal(rendererChanges.at(-1).type, 'webgl');
  assert.equal(errors.length, 1);
});

test('keeps settings changes independent from the renderer lifecycle', (t) => {
  useFakeTimers(t);
  const { view, activations, disposals } = setup(t);

  view.deactivate();
  view.applySettings({ theme: 'clearDark', fontSize: 15, cursorBlink: false, scrollback: 500 });
  assert.equal(activations.length, 1);
  t.mock.timers.tick(30_000);
  assert.equal(disposals.length, 1);

  view.applySettings({ theme: 'homebrew', fontSize: 14, cursorBlink: true, scrollback: 1000 });
  assert.equal(activations.length, 1);
  view.activate();
  assert.equal(activations.length, 2);
  assert.equal(view.terminal.options.fontSize, 14);
  assert.equal(view.terminal.options.scrollback, 1000);
});

test('dispose cancels pending release and makes lifecycle methods idempotent', (t) => {
  useFakeTimers(t);
  const previousCancelAnimationFrame = globalThis.cancelAnimationFrame;
  globalThis.cancelAnimationFrame = () => undefined;
  t.after(() => {
    if (previousCancelAnimationFrame === undefined) delete globalThis.cancelAnimationFrame;
    else globalThis.cancelAnimationFrame = previousCancelAnimationFrame;
  });
  const state = setup(t);
  const { view, activations, disposals, domRenderers, rendererChanges } = state;

  view.deactivate();
  view.dispose();
  view.dispose();
  view.activate();
  view.deactivate();
  t.mock.timers.tick(60_000);

  assert.equal(activations.length, 1);
  assert.equal(disposals.length, 1);
  assert.equal(domRenderers.length, 0);
  assert.equal(typeof rendererChanges.at(-1).renderRows, 'function');
  assert.equal(state.terminalDisposals, 1);
  assert.equal(state.removals, 1);
});
