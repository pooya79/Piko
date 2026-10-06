(() => {
  const studio = () => document.querySelector('[data-piko-studio]');
  const composer = () => studio()?.querySelector('[data-studio-form="message"]');
  let tab = 'preview';
  function showTab(name) {
    tab = name;
    studio()?.querySelectorAll('[data-pane-tab]').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.paneTab === tab)));
    studio()?.querySelectorAll('[data-pane-panel]').forEach(panel => { panel.hidden = panel.dataset.panePanel !== tab; });
  }
  window.pikoStudio?.registerPaneState('tabs', {
    capture: () => tab,
    restore: (_, previous) => showTab(previous),
  });
  // Interrupted historical requests may still carry selection metadata. Refresh
  // must not silently turn their preserved prose into an unscoped request.
  window.pikoStudio?.registerPaneState('selection', {
    capture() {
      const form = composer();
      return { key: form?.elements.selected_block.value, revision: form?.elements.selected_revision.value, label: form?.querySelector('[data-flow-request-label]').textContent };
    },
    restore(_, previous, admission = {}) {
      if (!previous.key || (admission.selection?.key === previous.key && String(admission.selection.revision) === previous.revision)) return;
      const form = composer();
      if (!form) return;
      form.elements.selected_block.value = previous.key;
      form.elements.selected_revision.value = previous.revision;
      form.querySelector('[data-flow-request-label]').textContent = previous.label;
      form.querySelector('[data-flow-request]').hidden = false;
      form.querySelector('[data-flow-request-stale]').hidden = previous.revision === studio().querySelector('[data-draft-revision]')?.dataset.draftRevision;
    },
  });
  document.addEventListener('click', event => {
    const button = event.target.closest('[data-pane-tab]');
    if (button) showTab(button.dataset.paneTab);
    // Historical targeted requests retain their explicit clear action.
    if (event.target.closest('[data-flow-clear]')) {
      const form = composer();
      if (!form) return;
      form.elements.selected_block.value = '';
      form.elements.selected_revision.value = '';
      form.querySelector('[data-flow-request]').hidden = true;
      studio().querySelector('[data-flow-stale-feedback]')?.remove();
      document.getElementById('builder-message').focus();
    }
  });
  document.addEventListener('piko:studio-view-updated', () => showTab(tab));
  document.addEventListener('piko:studio-updated', () => showTab(tab));
  showTab(tab);
})();
