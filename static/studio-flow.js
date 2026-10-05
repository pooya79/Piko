(() => {
  const studio = () => document.querySelector('[data-piko-studio]');
  const flow = () => studio()?.querySelector('[data-studio-flow]');
  const form = () => studio()?.querySelector('[data-studio-form="message"]');
  const blocks = () => [...(flow()?.querySelectorAll('[data-flow-key]') || [])];
  const block = key => blocks().find(node => node.dataset.flowKey === key);
  let tab = 'preview';
  function showTab(name) {
    tab = name;
    studio()?.querySelectorAll('[data-pane-tab]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.paneTab === tab)));
    studio()?.querySelectorAll('[data-pane-panel]').forEach(panel => { panel.hidden = panel.dataset.panePanel !== tab; });
    const viewport = flow()?.querySelector('.piko-flow-viewport');
    if (viewport?.getClientRects().length && !flow().dataset.flowInitialized) {
      flow().dataset.flowInitialized = 'true';
      viewport.scrollLeft = -(viewport.scrollWidth - viewport.clientWidth) / 2;
    }
  }
  function zoom(value) {
    if (!flow()) return;
    const viewport = flow().querySelector('.piko-flow-viewport');
    const width = viewport.scrollWidth - viewport.clientWidth;
    const position = width > 0 ? viewport.scrollLeft / width : -.5;
    flow().dataset.flowZoom = String(Math.min(150, Math.max(75, value)));
    flow().querySelector('[data-flow-zoom-value]').textContent = new Intl.NumberFormat('fa').format(Number(flow().dataset.flowZoom)) + '٪';
    viewport.scrollLeft = position * (viewport.scrollWidth - viewport.clientWidth);
  }
  function select(node) {
    blocks().forEach(other => { other.open = other === node; other.dataset.selected = String(other === node); });
  }
  function clear() {
    const current = form();
    if (!current) return;
    current.elements.selected_block.value = '';
    current.elements.selected_revision.value = '';
    current.querySelector('[data-flow-request]').hidden = true;
    current.querySelector('[data-flow-request-stale]').hidden = true;
  }
  function request(key) {
    const node = block(key);
    if (!node || !form()) return;
    select(node);
    flow().querySelector('[data-flow-stale]').hidden = true;
    form().elements.selected_block.value = key;
    form().elements.selected_revision.value = flow().dataset.flowRevision;
    form().querySelector('[data-flow-request-label]').textContent = node.querySelector('.piko-small-label').textContent + ' · ' + node.querySelector('strong').textContent;
    form().querySelector('[data-flow-request]').hidden = false;
    form().querySelector('[data-flow-request-stale]').hidden = true;
    studio().querySelector('[data-flow-stale-feedback]')?.remove();
    // Keep the owner's unsent prose and let them describe the change themselves.
    studio().querySelector('[data-studio-view="conversation"]').click();
    document.getElementById('builder-message').focus({ preventScroll: true });
  }
  window.pikoStudio?.registerPaneState('flow', {
    capture() {
      const viewport = flow()?.querySelector('.piko-flow-viewport');
      return {
        tab, bot: flow()?.dataset.flowBot, revision: flow()?.dataset.flowRevision,
        zoom: Number(flow()?.dataset.flowZoom || 100),
        selected: blocks().find(node => node.open)?.dataset.flowKey,
        request: form()?.elements.selected_block.value,
        requestRevision: form()?.elements.selected_revision.value,
        label: form()?.querySelector('[data-flow-request-label]')?.textContent,
        top: viewport?.scrollTop, left: viewport?.scrollLeft,
      };
    },
    restore(next, previous, admission = {}) {
      showTab(previous.tab);
      if (!flow() || flow().dataset.flowBot !== previous.bot) { clear(); return; }
      zoom(previous.zoom);
      const selected = block(previous.selected);
      if (selected) select(selected);
      if (previous.selected && (!selected || flow().dataset.flowRevision !== previous.revision)) {
        flow().querySelector(selected ? '[data-flow-notice]' : '[data-flow-stale]').hidden = false;
      }
      const accepted = admission.selection && previous.request === admission.selection.key && previous.requestRevision === admission.selection.revision;
      if (previous.request && !accepted) {
        const stale = !block(previous.request) || flow().dataset.flowRevision !== previous.requestRevision;
        // Keep stale request identity until the owner explicitly clears or replaces
        // it. A refresh must never turn targeted prose into an unscoped request.
        form().elements.selected_block.value = previous.request;
        form().elements.selected_revision.value = previous.requestRevision;
        form().querySelector('[data-flow-request-label]').textContent = previous.label;
        form().querySelector('[data-flow-request]').hidden = false;
        form().querySelector('[data-flow-request-stale]').hidden = !stale;
        if (stale) flow().querySelector('[data-flow-stale]').hidden = false;
      } else clear();
      const viewport = flow().querySelector('.piko-flow-viewport');
      if (viewport) { viewport.scrollTop = previous.top || 0; viewport.scrollLeft = previous.left || 0; }
    },
  });
  document.addEventListener('click', event => {
    const button = event.target.closest('[data-pane-tab]');
    if (button) { showTab(button.dataset.paneTab); return; }
    const action = event.target.closest('[data-flow-zoom-action]');
    if (action) { zoom(action.dataset.flowZoomAction === 'reset' ? 100 : Number(flow().dataset.flowZoom) + (action.dataset.flowZoomAction === 'in' ? 25 : -25)); return; }
    const change = event.target.closest('[data-flow-change]');
    if (change) { request(change.dataset.flowChange); return; }
    if (event.target.closest('[data-flow-clear]')) { clear(); studio().querySelector('[data-flow-stale-feedback]')?.remove(); document.getElementById('builder-message').focus(); return; }
    const summary = event.target.closest('[data-flow-key] > summary');
    if (summary) {
      // Native details remains the keyboard/no-script inspection alternative.
      blocks().forEach(node => { if (node !== summary.parentElement) node.open = false; node.dataset.selected = String(node === summary.parentElement); });
    }
    const edge = event.target.closest('.piko-flow-details a');
    if (edge) {
      const target = document.getElementById(decodeURIComponent(edge.getAttribute('href').slice(1)));
      if (target) { event.preventDefault(); select(target); target.querySelector('summary').focus({ preventScroll: true }); target.scrollIntoView({ block: 'nearest', inline: 'nearest' }); }
    }
  });
  // Preserve pane visibility and viewport when mobile changes the outer view.
  document.addEventListener('piko:studio-view-updated', () => showTab(tab));
  showTab(tab);
})();
