(() => {
  const root = () => document.querySelector('[data-piko-studio]');
  const key = () => 'piko-composer:' + root().dataset.draftKey;
  let submitted = false;
  let refreshing = false;
  const reading = {};
  function captureReading(studio) {
    for (const selector of ['.piko-studio-scroll', '.piko-studio-pane']) {
      const region = studio.querySelector(selector);
      // A hidden mobile region reports zero dimensions; retain its last visible state.
      if (region?.getClientRects().length) {
        reading[selector] = { top: region.scrollTop, atEnd: region.scrollHeight - region.clientHeight - region.scrollTop < 40 };
      }
    }
  }
  function restoreReading(studio) {
    for (const [selector, position] of Object.entries(reading)) {
      const region = studio.querySelector(selector);
      if (region?.getClientRects().length) region.scrollTop = position.atEnd ? region.scrollHeight : position.top;
    }
  }
  function saveDraft() {
    const composer = document.getElementById('builder-message');
    if (!composer || submitted) return;
    try { sessionStorage.setItem(key(), composer.value); } catch (_) { /* Editing works without storage. */ }
  }
  function enhance(restore = false) {
    const studio = root();
    if (!studio) return;
    studio.dataset.enhanced = '';
    studio.dataset.view ||= 'conversation';
    studio.querySelectorAll('[data-studio-view]').forEach(button => {
      button.setAttribute('aria-pressed', String(button.dataset.studioView === studio.dataset.view));
    });
    if (restore) {
      const composer = document.getElementById('builder-message');
      try {
        const saved = sessionStorage.getItem(key());
        if (!composer.value && saved) composer.value = saved;
      } catch (_) { /* A blocked storage API does not affect the composer. */ }
    }
  }
  async function refreshStudio() {
    const studio = root();
    if (!studio || refreshing) return;
    refreshing = true;
    try {
      const response = await fetch(studio.dataset.chatUrl, {
        headers: { 'X-Piko-Studio': 'fragment' }, cache: 'no-store',
      });
      if (!response.ok) throw new Error('studio unavailable');
      const next = new DOMParser().parseFromString(await response.text(), 'text/html').querySelector('[data-piko-studio]');
      if (!next) throw new Error('studio unavailable');
      // Read editing state after the request: typing may continue while it is in flight.
      const composer = document.getElementById('builder-message');
      captureReading(studio);
      const focused = document.activeElement;
      const state = {
        text: composer.value, start: composer.selectionStart, end: composer.selectionEnd,
        focusID: focused.id, focusInStudio: studio.contains(focused),
        view: studio.dataset.view,
        details: Object.fromEntries([...studio.querySelectorAll('details[id]')].map(detail => [detail.id, detail.open])),
      };
      studio.replaceWith(next);
      next.dataset.view = state.view;
      enhance();
      const input = document.getElementById('builder-message');
      input.value = state.text;
      input.setSelectionRange(state.start, state.end);
      next.querySelectorAll('details[id]').forEach(detail => { detail.open = state.details[detail.id] || false; });
      let focus = state.focusID ? document.getElementById(state.focusID) : null;
      if (state.focusInStudio && (!focus || focus.disabled)) {
        focus = state.view === 'pane' ? next.querySelector('#studio-pane') : input;
      }
      focus?.focus({ preventScroll: true });
      restoreReading(next);
      // The same chat may have become a Bot's Builder chat while work completed.
      history.replaceState(null, '', next.dataset.chatUrl);
      saveDraft();
      document.dispatchEvent(new Event('piko:studio-updated'));
    } catch (_) {
      const feedback = studio.querySelector('[data-studio-refresh-error]');
      if (feedback) feedback.hidden = false;
    } finally {
      refreshing = false;
    }
  }
  document.addEventListener('click', event => {
    const switcher = event.target.closest('[data-studio-view]');
    if (switcher) {
      const studio = root();
      captureReading(studio);
      studio.dataset.view = switcher.dataset.studioView;
      studio.querySelectorAll('[data-studio-view]').forEach(button => {
        button.setAttribute('aria-pressed', String(button === switcher));
      });
      document.getElementById(switcher.getAttribute('aria-controls')).focus({ preventScroll: true });
      restoreReading(studio);
      return;
    }
    if (event.target.closest('.piko-studio-chat-picker summary')) return;
    const picker = root()?.querySelector('.piko-studio-chat-picker');
    if (picker && !picker.contains(event.target)) picker.open = false;
    const example = event.target.closest('[data-example]');
    if (!example) return;
    const composer = document.getElementById('builder-message');
    composer.value = example.dataset.prompt;
    composer.focus();
    composer.setSelectionRange(composer.value.length, composer.value.length);
    composer.dispatchEvent(new Event('input', { bubbles: true }));
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') {
      const picker = root()?.querySelector('.piko-studio-chat-picker[open]');
      if (picker) { picker.open = false; picker.querySelector('summary').focus(); }
    }
    if (event.target.id !== 'builder-message' || event.isComposing || event.key !== 'Enter' || !(event.ctrlKey || event.metaKey)) return;
    event.preventDefault();
    const form = event.target.form;
    if (form.querySelector('button[type="submit"]:disabled')) return;
    form.requestSubmit();
  });
  document.addEventListener('submit', event => {
    if (!event.target.querySelector('#builder-message')) return;
    if (!event.target.hasAttribute('action')) { event.preventDefault(); return; }
    submitted = true;
    try { sessionStorage.removeItem(key()); } catch (_) { /* Optional persistence. */ }
    // Prevent a double click from creating two conversations from the welcome.
    event.target.querySelector('button[type="submit"]').disabled = true;
  });
  document.addEventListener('input', event => {
    if (event.target.id === 'builder-message') { submitted = false; saveDraft(); }
  });
  document.addEventListener('piko:run-complete', refreshStudio);
  window.addEventListener('pagehide', saveDraft);
  window.addEventListener('pageshow', () => {
    submitted = false;
    const button = root()?.querySelector('[data-can-send]');
    if (button) button.disabled = button.dataset.canSend !== 'true';
  });
  enhance(true);
})();
