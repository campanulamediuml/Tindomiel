import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { registerHooks } from 'node:module';
import ts from 'typescript';

const treeUrl = new URL('./tree.ts', import.meta.url).href;
const sftpUrl = new URL('./sftp.ts', import.meta.url).href;
const utilsUrl = new URL('./utils.ts', import.meta.url).href;
const typesUrl = new URL('./types.ts', import.meta.url).href;
const sourceUrls = new Set([treeUrl, sftpUrl, utilsUrl, typesUrl]);

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (context.parentURL === sftpUrl && specifier === './tree') return { url: treeUrl, shortCircuit: true };
    if ((context.parentURL === sftpUrl || context.parentURL === treeUrl) && specifier === './utils') {
      return { url: utilsUrl, shortCircuit: true };
    }
    if (context.parentURL === sftpUrl && specifier === './types') return { url: typesUrl, shortCircuit: true };
    return nextResolve(specifier, context);
  },
  load(url, context, nextLoad) {
    if (!sourceUrls.has(url)) return nextLoad(url, context);
    return {
      format: 'module',
      source: ts.transpileModule(readFileSync(fileURLToPath(url), 'utf8'), {
        compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
      }).outputText,
      shortCircuit: true,
    };
  },
});

const { RemoteTree } = await import(treeUrl);
const { SftpPanel } = await import(sftpUrl);

class FakeElement {
  constructor() {
    this.children = [];
    this.style = {};
    this.className = '';
    this.textContent = '';
    this.title = '';
  }

  append(...children) {
    this.children.push(...children);
  }

  replaceChildren(...children) {
    this.children = children;
    this.textContent = '';
  }

  addEventListener() {}
}

function defineGlobal(t, name, value) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor);
    else delete globalThis[name];
  });
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function directory(name) {
  return { name, type: 'directory', size: 0, mtime: 0, created: 0, permissions: 'drwxr-xr-x' };
}

function settle() {
  return new Promise((resolve) => setImmediate(resolve));
}

function treeHarness(t, listFiles) {
  defineGlobal(t, 'document', { createElement: () => new FakeElement() });
  const errors = [];
  const tree = Object.create(RemoteTree.prototype);
  Object.assign(tree, {
    sessionId: undefined,
    state: undefined,
    snapshots: new Map(),
    container: new FakeElement(),
    api: { listFiles },
    onSelect() {},
    onError(message) { errors.push(message); },
  });
  return { tree, errors };
}

function sftpHarness(t, listFiles) {
  defineGlobal(t, 'document', { createElement: () => new FakeElement() });
  const errors = [];
  const renders = [];
  const directoryUpdates = [];
  const panel = Object.create(SftpPanel.prototype);
  Object.assign(panel, {
    sessionId: undefined,
    currentPath: '/',
    views: new Map([
      ['A', { path: '/', entries: [directory('a')], loading: false }],
      ['B', { path: '/', entries: [directory('b')], loading: false }],
    ]),
    entries: [],
    selected: new Set(),
    fileTypes: new Map(),
    loadVersions: new Map(),
    pathInput: { value: '' },
    fileList: new FakeElement(),
    api: { realpath: async (_sessionId, path) => path, listFiles },
    tree: {
      async setSession() {},
      setCurrent() {},
      setDirectory(...args) { directoryUpdates.push(args); },
      removeSession() {},
    },
    onError(message) { errors.push(message); },
  });
  panel.render = () => {
    panel.fileList.replaceChildren();
    renders.push(panel.sessionId);
  };
  return { panel, errors, renders, directoryUpdates };
}

test('tree initially loads only root and loads children when the user expands a directory', async (t) => {
  const requests = [];
  const { tree } = treeHarness(t, async (sessionId, path) => {
    requests.push([sessionId, path]);
    if (path === '/') return [directory('home')];
    if (path === '/home') return [directory('ubuntu')];
    return [];
  });

  await tree.setSession('A');
  await settle();

  assert.deepEqual(requests, [['A', '/']]);
  const homeRow = tree.container.children.find((row) => row.children[1]?.textContent === 'home');
  assert.match(homeRow.className, /unloaded/);

  await tree.toggle('/home');

  assert.deepEqual(requests, [['A', '/'], ['A', '/home']]);
  const loadedHomeRow = tree.container.children.find((row) => row.children[1]?.textContent === 'home');
  assert.doesNotMatch(loadedHomeRow.className, /unloaded/);
  assert.deepEqual(tree.state.nodes.get('/home').children.map((entry) => entry.name), ['ubuntu']);
});

test('path synchronization does not fetch or expand ancestors and refresh fetches only user-expanded paths', async (t) => {
  const requests = [];
  const { tree } = treeHarness(t, async (_sessionId, path) => {
    requests.push(path);
    return path === '/' ? [directory('home')] : [];
  });
  await tree.setSession('A');
  await settle();
  requests.length = 0;

  tree.setCurrent('/home/ubuntu');
  assert.deepEqual(requests, []);
  assert.deepEqual([...tree.state.expanded], ['/']);

  await tree.refresh();
  assert.deepEqual(requests, ['/']);

  await tree.toggle('/home');
  tree.setCurrent('/home/ubuntu');
  requests.length = 0;
  await tree.refresh();
  assert.deepEqual(requests.sort(), ['/', '/home'].sort());
});

test('tree completion updates its original session without rendering into another session', async (t) => {
  const home = deferred();
  const requests = [];
  const { tree, errors } = treeHarness(t, async (sessionId, path) => {
    requests.push([sessionId, path]);
    if (path === '/home') return home.promise;
    return sessionId === 'A' ? [directory('home')] : [directory('b-only')];
  });
  await tree.setSession('A');
  await settle();
  const loading = tree.toggle('/home');
  await tree.setSession('B');
  await settle();

  home.resolve([directory('ubuntu')]);
  await loading;

  assert.deepEqual(tree.state.nodes.get('/').children.map((entry) => entry.name), ['b-only']);
  assert.equal(tree.container.children.some((row) => row.children[1]?.textContent === 'ubuntu'), false);
  assert.deepEqual(tree.snapshots.get('A').nodes.get('/home').children.map((entry) => entry.name), ['ubuntu']);
  assert.deepEqual(errors, []);

  await tree.setSession('A');
  await tree.toggle('/home');
  await tree.toggle('/home');
  assert.equal(requests.filter(([, path]) => path === '/home').length, 1, 'loaded children are reused');
});

test('SFTP completion updates its original session while another session is active', async (t) => {
  const listing = deferred();
  const { panel, errors, directoryUpdates } = sftpHarness(t, () => listing.promise);
  await panel.setSession('A');
  const loading = panel.load('/home');
  await settle();
  await panel.setSession('B');

  listing.resolve([directory('ubuntu')]);
  await loading;

  assert.equal(panel.sessionId, 'B');
  assert.equal(panel.currentPath, '/');
  assert.equal(panel.views.get('A').path, '/home');
  assert.equal(panel.views.get('A').loading, false);
  assert.deepEqual(panel.views.get('A').entries, [directory('ubuntu')]);
  assert.deepEqual(directoryUpdates, [['A', '/home', [directory('ubuntu')]]]);
  assert.deepEqual(errors, []);

  await panel.setSession('A');
  assert.equal(panel.currentPath, '/home');
  assert.deepEqual(panel.entries, [directory('ubuntu')]);
});

test('SFTP restores a pending view and renders its result when the session returns', async (t) => {
  const listing = deferred();
  const { panel, renders } = sftpHarness(t, () => listing.promise);
  await panel.setSession('A');
  const loading = panel.load('/home');
  await settle();
  await panel.setSession('B');
  await panel.setSession('A');

  assert.equal(panel.currentPath, '/home');
  assert.equal(panel.fileList.textContent, '读取中…');
  listing.resolve([directory('ubuntu')]);
  await loading;

  assert.equal(panel.views.get('A').loading, false);
  assert.equal(panel.fileList.textContent, '');
  assert.equal(renders.at(-1), 'A');
});

test('SFTP ignores stale responses independently within each session', async (t) => {
  const requests = new Map();
  const { panel } = sftpHarness(t, (_sessionId, path) => {
    const request = deferred();
    requests.set(path, request);
    return request.promise;
  });
  await panel.setSession('A');
  const first = panel.load('/first');
  await settle();
  const second = panel.load('/second');
  await settle();

  requests.get('/second').resolve([directory('new')]);
  await second;
  requests.get('/first').resolve([directory('old')]);
  await first;

  assert.equal(panel.views.get('A').path, '/second');
  assert.deepEqual(panel.views.get('A').entries, [directory('new')]);
});
