(() => {
  const root = () => document.querySelector('[data-piko-studio]');
  const key = () => 'piko-composer:' + root().dataset.draftKey;
  let refreshing = false;
  let renderedSignature = root()?.outerHTML;
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
    if (!composer) return;
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
  // Pane slices register their own state at the same committed refresh boundary.
  const paneStates = new Map();
  window.pikoStudio = {
    registerPaneState(name, adapter) {
      paneStates.set(name, adapter);
      return () => { if (paneStates.get(name) === adapter) paneStates.delete(name); };
    },
    refresh: () => refreshStudio(),
    whenCompositionEnds: () => waitForComposition(),
  };
  let queue = Promise.resolve();
  let composing = false;
  const compositionWaiters = [];
  function waitForComposition() {
    return composing ? new Promise(resolve => compositionWaiters.push(resolve)) : Promise.resolve();
  }
  let posting = false;
  function enqueue(work) {
    queue = queue.then(work).catch(() => {
      const feedback = root()?.querySelector('[data-studio-refresh-error]');
      if (feedback) feedback.hidden = false;
    });
    return queue;
  }
  async function readFragment(response) {
    // Error fragments carry validation/conflict feedback; an auth/error document
    // cannot replace the studio or cause an automatic repeat of a POST.
    const next = new DOMParser().parseFromString(await response.text(), 'text/html').querySelector('[data-piko-studio]');
    if (!next) throw new Error('studio unavailable');
    return next;
  }
  function applyFragment(next, acceptedMessage, acceptedSelection) {
    const studio = root();
    const signature = next.outerHTML;
    const oldKey = key();
    const composer = document.getElementById('builder-message');
    captureReading(studio);
    const focused = document.activeElement;
    const state = {
      text: composer.value, start: composer.selectionStart, end: composer.selectionEnd,
      direction: composer.selectionDirection, scroll: composer.scrollTop,
      focusID: focused.id, focusInStudio: studio.contains(focused),
      view: studio.dataset.view || 'conversation',
      details: Object.fromEntries([...studio.querySelectorAll('details[id]')].map(detail => [detail.id, detail.open])),
    };
    const paneSnapshots = [...paneStates].map(([name, adapter]) => [name, adapter.capture(studio)]);
    // Only clear the admitted text. A follow-up typed during the request survives.
    if (acceptedMessage !== undefined && state.text === acceptedMessage) {
      state.text = ''; state.start = state.end = 0;
    }
    studio.replaceWith(next);
    next.dataset.view = state.view;
    enhance();
    const input = document.getElementById('builder-message');
    input.value = state.text;
    input.setSelectionRange(state.start, state.end, state.direction);
    input.scrollTop = state.scroll;
    next.querySelectorAll('details[id]').forEach(detail => { detail.open = state.details[detail.id] || false; });
    for (const [name, snapshot] of paneSnapshots) paneStates.get(name)?.restore(next, snapshot, { message: acceptedMessage, selection: acceptedSelection });
    let focus = state.focusID ? document.getElementById(state.focusID) : null;
    if (state.focusInStudio && (!focus || focus.disabled)) {
      focus = state.view === 'pane' ? next.querySelector('#studio-pane') : input;
    }
    focus?.focus({ preventScroll: true });
    restoreReading(next);
    // The conversation may now identify a newly committed Bot.
    history.replaceState(history.state, '', next.dataset.chatUrl);
    try { if (oldKey !== key()) sessionStorage.removeItem(oldKey); } catch (_) { /* Optional persistence. */ }
    saveDraft();
    renderedSignature = signature;
    document.dispatchEvent(new CustomEvent('piko:studio-updated', { detail: { studio: next } }));
  }
  function refreshStudio() {
    if (!root() || refreshing) return queue;
    refreshing = true;
    return enqueue(async () => {
      try {
        await waitForComposition();
        const response = await fetch(root().dataset.chatUrl, {
          headers: { 'X-Piko-Studio': 'fragment' }, cache: 'no-store', mode: 'same-origin', signal: AbortSignal.timeout(10000),
        });
        if (!response.ok) throw new Error('studio unavailable');
        const next = await readFragment(response);
        await waitForComposition();
        if (next.outerHTML !== renderedSignature) applyFragment(next);
        else {
          const feedback = root().querySelector('[data-studio-refresh-error]');
          if (feedback) feedback.hidden = true;
        }
      } finally { refreshing = false; }
    });
  }
  async function submitForm(form) {
    if (posting) return;
    posting = true;
    const body = new URLSearchParams(new FormData(form));
    const message = form.dataset.studioForm === 'message' ? body.get('message') : undefined;
    const button = form.querySelector('button[type="submit"]');
    // Keep focus on an action while its request is pending. Disabling a focused
    // native button moves focus to body before the refresh can capture it.
    button.setAttribute('aria-disabled', 'true');
    form.setAttribute('aria-busy', 'true');
    await enqueue(async () => {
      try {
        const response = await fetch(form.action, {
          method: 'POST', body, headers: { 'X-Piko-Studio': 'fragment' },
          cache: 'no-store', mode: 'same-origin', signal: AbortSignal.timeout(10000),
        });
        const next = await readFragment(response);
        // Preserve an active composition until its text is committed by the IME.
        await waitForComposition();
        const accepted = response.headers.get('X-Piko-Accepted') === 'true';
        applyFragment(next, accepted ? message : undefined, accepted && message !== undefined ? { key: body.get('selected_block'), revision: body.get('selected_revision') } : undefined);
      } finally {
        posting = false;
        button.removeAttribute('aria-disabled');
        form.removeAttribute('aria-busy');
      }
    });
  }
  document.addEventListener('click', event => {
    if (event.target.closest('[data-studio-refresh]')) { refreshStudio(); return; }
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
      document.dispatchEvent(new CustomEvent('piko:studio-view-updated'));
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
    const form = event.target;
    if (!form.matches('[data-studio-form]')) return;
    event.preventDefault();
    if (form.hasAttribute('action')) submitForm(form);
  });
  document.addEventListener('compositionstart', () => { composing = true; });
  document.addEventListener('compositionend', () => {
    composing = false;
    for (const resolve of compositionWaiters.splice(0)) resolve();
  });
  document.addEventListener('input', event => {
    if (event.target.id === 'builder-message') { saveDraft(); }
  });
  document.addEventListener('piko:run-complete', refreshStudio);
  window.addEventListener('pagehide', saveDraft);
  window.addEventListener('pageshow', () => {
    const button = root()?.querySelector('[data-can-send]');
    if (button) button.disabled = button.dataset.canSend !== 'true';
    if (root()?.dataset.chatUrl !== '/builder') refreshStudio();
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && root()?.dataset.chatUrl !== '/builder') refreshStudio();
  });
  // Read-only reconciliation covers lost SSE, unavailable EventSource, and work
  // admitted in another browser/chat. It never submits or retries a request.
  setInterval(() => {
    if (!document.hidden && !posting && root()?.dataset.chatUrl !== '/builder') refreshStudio();
  }, 5000);
  enhance(true);
})();
