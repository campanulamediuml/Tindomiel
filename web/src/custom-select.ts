export class CustomSelect {
  private readonly button: HTMLButtonElement;
  private readonly menu: HTMLDivElement;

  constructor(private readonly select: HTMLSelectElement) {
    select.classList.add('native-select');
    const wrapper = document.createElement('div');
    wrapper.className = 'custom-select';
    this.button = document.createElement('button');
    this.button.type = 'button';
    this.button.className = 'select-button';
    this.button.setAttribute('aria-haspopup', 'listbox');
    this.menu = document.createElement('div');
    this.menu.className = 'select-menu';
    this.menu.setAttribute('role', 'listbox');
    this.menu.hidden = true;
    wrapper.append(this.button, this.menu);
    select.after(wrapper);
    this.sync();
    select.addEventListener('change', () => this.sync());
    new MutationObserver(() => this.sync()).observe(select, { childList: true });
    this.button.addEventListener('click', (event) => {
      event.stopPropagation();
      this.menu.hidden = !this.menu.hidden;
      this.button.setAttribute('aria-expanded', String(!this.menu.hidden));
    });
    this.menu.addEventListener('click', (event) => event.stopPropagation());
    document.addEventListener('click', () => this.close());
    this.button.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') this.close();
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        this.menu.hidden = false;
        this.menu.querySelector<HTMLButtonElement>('.select-option.selected, .select-option:not(:disabled)')?.focus();
      }
    });
  }

  private sync(): void {
    const selected = this.select.selectedOptions[0];
    this.button.replaceChildren();
    const label = document.createElement('span');
    label.textContent = selected?.textContent ?? '请选择';
    const arrow = document.createElement('span');
    arrow.textContent = '▾';
    this.button.append(label, arrow);
    this.button.disabled = this.select.disabled;
    this.menu.replaceChildren();
    for (const option of Array.from(this.select.options)) {
      const item = document.createElement('button');
      item.type = 'button';
      item.className = `select-option ${option.selected ? 'selected' : ''}`;
      item.setAttribute('role', 'option');
      item.setAttribute('aria-selected', String(option.selected));
      item.textContent = option.textContent;
      item.disabled = option.disabled;
      item.addEventListener('click', () => {
        this.select.value = option.value;
        this.select.dispatchEvent(new Event('change', { bubbles: true }));
        this.sync();
        this.close();
      });
      this.menu.append(item);
    }
  }

  private close(): void {
    this.menu.hidden = true;
    this.button.setAttribute('aria-expanded', 'false');
  }
}
