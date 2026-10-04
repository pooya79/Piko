(() => {
  const marker = document.querySelector('[data-builder-status-url]');
  if (!marker) return;
  let stopped = false;
  let timer;
  window.addEventListener('pagehide', () => {
    stopped = true;
    clearTimeout(timer);
  });
  window.addEventListener('pageshow', (event) => {
    if (event.persisted) {
      stopped = false;
      timer = setTimeout(refresh, 0);
    }
  });
  async function refresh() {
    if (stopped) return;
    try {
      if (!document.hidden) {
        const response = await fetch(marker.dataset.builderStatusUrl, {
          cache: 'no-store', credentials: 'same-origin', redirect: 'error',
          signal: AbortSignal.timeout(5000),
        });
        if (response.status === 404 || response.status === 401 || response.status === 403) return;
        if (response.ok) {
          const run = await response.json();
          if (String(run.id) !== marker.dataset.builderRunId || run.status !== 'running') {
            window.location.reload();
            return;
          }
        }
      }
    } catch {
      // A display outage never stops the run. The manual refresh link also works.
    }
    if (!stopped) timer = setTimeout(refresh, 2000);
  }
  timer = setTimeout(refresh, 2000);
})();
