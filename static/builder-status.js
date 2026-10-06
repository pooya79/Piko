(() => {
  let stream;
  function connect() {
    stream?.close();
    const marker = document.querySelector('[data-builder-stream-url]');
    if (!marker) return;
    const progress = document.querySelector('[data-builder-progress]');
    const reply = document.querySelector('[data-builder-reply]');
    const connection = document.querySelector('[data-builder-connection]');
    if (!window.EventSource) {
      if (connection) connection.textContent = connection.dataset.unavailable;
      return;
    }
    const source = new EventSource(marker.dataset.builderStreamUrl);
    stream = source;
    source.addEventListener('snapshot', event => {
      if (!marker.isConnected || stream !== source) return;
      const run = JSON.parse(event.data);
      if (connection) connection.textContent = '';
      if (String(run.id) !== marker.dataset.builderRunId || run.status !== 'running') {
        source.close();
        document.dispatchEvent(new Event('piko:run-complete'));
        return;
      }
      const history = marker.querySelector('.piko-studio-scroll');
      const follow = history?.getClientRects().length && history.scrollHeight - history.clientHeight - history.scrollTop < 40;
      // Provisional text is plain text, never HTML or committed Draft feedback.
      if (reply) reply.textContent = run.text;
      if (progress && run.progress) progress.textContent = run.progress;
      if (follow) history.scrollTop = history.scrollHeight;
    });
    source.addEventListener('error', () => {
      if (!marker.isConnected || stream !== source) return;
      if (connection) connection.textContent = source.readyState === EventSource.CLOSED
        ? connection.dataset.unavailable : connection.dataset.reconnecting;
    });
  }
  window.addEventListener('pagehide', () => stream?.close());
  window.addEventListener('pageshow', event => { if (event.persisted) connect(); });
  document.addEventListener('piko:studio-updated', connect);
  connect();
})();
