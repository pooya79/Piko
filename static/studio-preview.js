(() => {
  const studio = () => document.querySelector('[data-piko-studio]');
  const pane = () => studio()?.querySelector('[data-studio-preview]');
  const storageKey = () => 'piko-preview:' + studio().dataset.draftKey + ':' + pane().dataset.previewBase;
  let pending = false;
  let retryURL;
  let reading;
  function captureReading() {
    const transcript = pane()?.querySelector('.piko-preview-transcript');
    if (transcript?.getClientRects().length) reading = {
      top: transcript.scrollTop,
      atEnd: transcript.scrollHeight - transcript.clientHeight - transcript.scrollTop < 40,
    };
  }
  function restoreReading() {
    const transcript = pane()?.querySelector('.piko-preview-transcript');
    if (reading && transcript?.getClientRects().length) transcript.scrollTop = reading.atEnd ? transcript.scrollHeight : reading.top;
  }
  function markRevision() {
    const current = studio()?.querySelector('[data-draft-revision]')?.dataset.draftRevision;
    const source = pane()?.querySelector('[data-preview-source-revision]');
    if (!source || !current) return;
    // A Preview action can observe a commit before the studio has reconciled.
    const latest = BigInt(current) > BigInt(source.dataset.previewDraftRevision) ? current : source.dataset.previewDraftRevision;
    const stale = source.dataset.previewSourceRevision === '0' || source.dataset.previewSourceRevision !== latest;
    source.dataset.previewStale = String(stale);
    source.querySelector('[data-preview-stale-notice]').hidden = !stale;
  }
  function saveSelection() {
    try { sessionStorage.setItem(storageKey(), pane().dataset.previewUrl); } catch (_) { /* Optional reload recovery. */ }
  }
  // Keep the actual DOM: unsent answers, transcript position and pending form
  // references survive run fragments. Only an explicit Preview action changes it.
  window.pikoStudio?.registerPaneState('preview', {
    capture: () => pane(),
    restore(next, previous) {
      const placeholder = next.querySelector('[data-studio-preview]');
      if (previous && placeholder?.dataset.previewBase === previous.dataset.previewBase &&
          (previous.dataset.previewUrl !== previous.dataset.previewBase || pending)) {
        placeholder.replaceWith(previous);
      }
      markRevision();
    },
  });
  async function load(url, body, focus = false) {
    if (pending || !pane()) return;
    pending = true;
    const previous = pane();
    const actionFocus = document.activeElement;
    captureReading();
    previous.setAttribute('aria-busy', 'true');
    previous.querySelector('[data-preview-activity]').hidden = false;
    previous.querySelectorAll('button[type="submit"]').forEach(button => button.setAttribute('aria-disabled', 'true'));
    try {
      const response = await fetch(url, {
        method: body ? 'POST' : 'GET', body,
        headers: { 'X-Piko-Preview': 'fragment' },
        mode: 'same-origin', cache: 'no-store', signal: AbortSignal.timeout(10000),
      });
      const next = new DOMParser().parseFromString(await response.text(), 'text/html').querySelector('[data-studio-preview]');
      if (!next || next.dataset.previewBase !== previous.dataset.previewBase ||
          (!response.ok && ![409, 422].includes(response.status))) throw new Error('Preview unavailable');
      await window.pikoStudio.whenCompositionEnds();
      // The owner may have navigated while this request was in flight.
      if (pane()?.dataset.previewBase !== previous.dataset.previewBase) return;
      const answer = previous.querySelector('#preview-answer');
      const nextAnswer = next.querySelector('#preview-answer');
      // Validation and conflict feedback leave the owner's unsent answer intact.
      const sameProgress = previous.querySelector('[data-preview-revision]')?.dataset.previewRevision === next.querySelector('[data-preview-revision]')?.dataset.previewRevision;
      if (answer && nextAnswer && (!response.ok || (!body && sameProgress))) {
        nextAnswer.value = answer.value;
        nextAnswer.setSelectionRange(answer.selectionStart, answer.selectionEnd, answer.selectionDirection);
        nextAnswer.scrollTop = answer.scrollTop;
      }
      captureReading();
      if (next.dataset.previewUrl !== previous.dataset.previewUrl) reading = { top: 0, atEnd: true };
      const currentFocus = document.activeElement;
      const focusWithin = previous.contains(currentFocus) && previous.getClientRects().length > 0;
      const retainedFocus = sameProgress && focusWithin && currentFocus.id ? [...next.querySelectorAll('[id]')].find(el => el.id === currentFocus.id) : null;
      // A delayed response respects focus moved to the composer or another view.
      const focusNext = focus && currentFocus === actionFocus && focusWithin;
      pane().replaceWith(next);
      retryURL = undefined;
      markRevision();
      saveSelection();
      restoreReading();
      if (retainedFocus) retainedFocus.focus({ preventScroll: true });
      else if (focusNext) (nextAnswer || next.querySelector('.piko-preview-buttons button') || next.querySelector('button'))?.focus({ preventScroll: true });
    } catch (_) {
      // A lost POST response must be recovered by observing the saved Preview,
      // never by automatically repeating the mutation.
      retryURL = previous.dataset.previewUrl;
      if (pane()) pane().querySelector('[data-preview-load-error]').hidden = false;
    } finally {
      pending = false;
      previous.removeAttribute('aria-busy');
      previous.querySelector('[data-preview-activity]').hidden = true;
      previous.querySelectorAll('[aria-disabled]').forEach(button => button.removeAttribute('aria-disabled'));
    }
  }
  document.addEventListener('submit', event => {
    const form = event.target;
    if (!form.closest('[data-piko-studio] [data-studio-preview]')) return;
    event.preventDefault();
    if (pending) return;
    const body = new URLSearchParams(new FormData(form));
    if (event.submitter?.name) body.set(event.submitter.name, event.submitter.value);
    load(form.action, body, true);
  });
  document.addEventListener('click', event => {
    if (event.target.closest('[data-preview-retry]')) load(retryURL || pane().dataset.previewUrl, undefined, true);
  });
  document.addEventListener('piko:studio-updated', markRevision);
  document.addEventListener('scroll', event => {
    if (event.target.matches?.('.piko-preview-transcript')) captureReading();
  }, true);
  document.addEventListener('click', event => {
    if (event.target.closest('[data-studio-view]')) captureReading();
  }, true);
  document.addEventListener('piko:studio-view-updated', restoreReading);
  if (pane()) {
    let selected;
    try { selected = sessionStorage.getItem(storageKey()); } catch (_) { /* Storage may be unavailable. */ }
    // Only recover this owner's locally selected Preview on the same Bot.
    if (selected?.startsWith(pane().dataset.previewBase + '/') && /^[-\w]+$/.test(selected.slice(pane().dataset.previewBase.length + 1))) load(selected);
  }
})();
