import { element } from './utils';

export function setupResizing(): void {
  const workspace = element<HTMLElement>('workspace');
  const files = element<HTMLElement>('files-workspace');
  setupPanelResizer('sidebar-resizer', (startX, _startY, initial) => (event) => {
    workspace.style.setProperty('--sidebar-width', `${clamp(initial + event.clientX - startX, 180, 480)}px`);
  }, Number.parseFloat(getComputedStyle(workspace).getPropertyValue('--sidebar-width')));
  setupPanelResizer('tree-resizer', (startX, _startY, initial) => (event) => {
    files.style.setProperty('--tree-width', `${clamp(initial + event.clientX - startX, 160, 520)}px`);
  }, Number.parseFloat(getComputedStyle(files).getPropertyValue('--tree-width')));
  setupPanelResizer('files-resizer', (_startX, startY, initial) => (event) => {
    const maximum = Math.max(200, workspace.clientHeight - 200);
    workspace.style.setProperty('--files-height', `${clamp(initial - (event.clientY - startY), 200, maximum)}px`);
  }, Number.parseFloat(getComputedStyle(workspace).getPropertyValue('--files-height')));
  setupFileColumnResizing();
}

function setupPanelResizer(id: string, createMove: (startX: number, startY: number, initial: number) => (event: PointerEvent) => void, initial: number): void {
  const handle = element<HTMLElement>(id);
  handle.addEventListener('pointerdown', (event) => {
    const move = createMove(event.clientX, event.clientY, initial);
    beginDrag(handle, event, move);
  });
}

function setupFileColumnResizing(): void {
  const header = element<HTMLElement>('file-header');
  const dropzone = element<HTMLElement>('sftp-dropzone');
  for (const handle of header.querySelectorAll<HTMLElement>('.column-resizer')) {
    handle.addEventListener('pointerdown', (event) => {
      event.stopPropagation();
      const columns = getComputedStyle(header).gridTemplateColumns.split(' ');
      const names = ['name', 'size', 'mtime', 'ctime', 'permissions'];
      const index = names.indexOf(handle.dataset.column ?? '');
      if (index < 0) return;
      const initial = Number.parseFloat(columns[index] ?? '0');
      const startX = event.clientX;
      beginDrag(handle, event, (moveEvent) => {
        const width = clamp(initial + moveEvent.clientX - startX, index === 0 ? 120 : 55, 700);
        dropzone.style.setProperty(`--col-${names[index]}`, `${width}px`);
      });
    });
  }
}

function beginDrag(handle: HTMLElement, event: PointerEvent, onMove: (event: PointerEvent) => void): void {
  event.preventDefault();
  handle.setPointerCapture(event.pointerId);
  handle.classList.add('active');
  handle.addEventListener('pointermove', onMove);
  const finish = (finishEvent: PointerEvent) => {
    if (handle.hasPointerCapture(finishEvent.pointerId)) handle.releasePointerCapture(finishEvent.pointerId);
    handle.classList.remove('active');
    handle.removeEventListener('pointermove', onMove);
    handle.removeEventListener('pointerup', finish);
    handle.removeEventListener('pointercancel', finish);
  };
  handle.addEventListener('pointerup', finish);
  handle.addEventListener('pointercancel', finish);
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.min(maximum, Math.max(minimum, value));
}
